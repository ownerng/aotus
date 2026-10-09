package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
)

// rig is a manager over real storage whose employees run the fake CLI.
type rig struct {
	*env
	mgr *Manager
}

func newRig(t *testing.T, opts Options) *rig {
	t.Helper()
	e := newEnv(t)
	return &rig{env: e, mgr: newManagerFor(t, e, opts)}
}

func newManagerFor(t *testing.T, e *env, opts Options) *Manager {
	t.Helper()
	m := NewManager(e.svc, map[provider.Kind]provider.Provider{
		provider.KindClaude: provider.Claude{},
		provider.KindCodex:  provider.Codex{},
	}, opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
	})
	return m
}

// profile adds a Claude Code profile whose CLI is the fake one.
func (r *rig) profile(t *testing.T, name string, sc providertest.Scenario, mode provider.Mode) provider.Profile {
	t.Helper()
	p, err := provider.NewProfile(r.layout, provider.KindClaude, name, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	p.Mode = mode
	p.AcceptedNotices = []string{provider.NoticeClaudeHeadless}
	p.ExtraEnv = map[string]string{
		providertest.EnvFakeCLI:      "1",
		providertest.EnvFakeScenario: string(sc),
		providertest.EnvFakePidFile:  filepath.Join(t.TempDir(), "pids"),
	}
	if err := r.svc.AddProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *rig) employee(t *testing.T, name string, p provider.Profile) Employee {
	t.Helper()
	emp, err := r.svc.CreateEmployee(context.Background(), NewEmployee{Name: name, Role: "tester", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return emp
}

// worker is an employee that runs structured turns of the given scenario.
func (r *rig) worker(t *testing.T, name string, sc providertest.Scenario) (Employee, provider.Profile) {
	t.Helper()
	p := r.profile(t, name, sc, provider.ModeStructured)
	return r.employee(t, name, p), p
}

// waitUpdate returns the first update that satisfies match, failing the test
// if none arrives in time.
func waitUpdate(t *testing.T, ch <-chan Update, match func(Update) bool) Update {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case u, ok := <-ch:
			if !ok {
				t.Fatal("the update stream closed while waiting")
			}
			if match(u) {
				return u
			}
		case <-deadline:
			t.Fatal("timed out waiting for an update")
		}
	}
}

func turnEnded(id string) func(Update) bool {
	return func(u Update) bool { return u.Kind == UpdateTurnEnded && u.TurnID == id }
}

func TestTurnStreamsEventsInOrder(t *testing.T) {
	r := newRig(t, Options{})
	emp, _ := r.worker(t, "Atlas", providertest.Hello)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()

	turnID, err := r.mgr.Send(context.Background(), emp.ID, "ping")
	if err != nil {
		t.Fatal(err)
	}
	var updates []Update
	for {
		u := waitUpdate(t, sub, func(Update) bool { return true })
		updates = append(updates, u)
		if u.Kind == UpdateTurnEnded {
			break
		}
	}

	var kinds []string
	var events []provider.Event
	for i, u := range updates {
		kinds = append(kinds, string(u.Kind))
		if u.TurnID != turnID || u.EmployeeID != emp.ID {
			t.Errorf("update %d belongs to %s/%s, want %s/%s", i, u.EmployeeID, u.TurnID, emp.ID, turnID)
		}
		if i > 0 && u.Seq != updates[i-1].Seq+1 {
			t.Errorf("sequence numbers must increase by one: %d after %d", u.Seq, updates[i-1].Seq)
		}
		if u.Event != nil {
			events = append(events, *u.Event)
		}
	}
	if len(kinds) < 5 || kinds[0] != "turn_queued" || kinds[1] != "turn_started" || kinds[len(kinds)-1] != "turn_ended" {
		t.Fatalf("update kinds = %v, want turn_queued, turn_started, events..., turn_ended", kinds)
	}
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("the streamed events violate the contract: %v\n%v", err, events)
	}
	var text string
	for _, e := range events {
		if e.Kind == provider.EventText {
			text += e.Text
		}
	}
	if text != "pong" {
		t.Fatalf("streamed text = %q, want pong", text)
	}
	if last := updates[len(updates)-1]; last.State != store.TurnCompleted {
		t.Fatalf("the turn ended as %q", last.State)
	}
	if st := r.mgr.Status(emp.ID); st.Working || st.Mode != provider.ModeStructured {
		t.Fatalf("status after the turn = %+v", st)
	}
}

func TestTurnPersistsHistory(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	emp, _ := r.worker(t, "Atlas", providertest.Hello)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()

	var ids []string
	for _, prompt := range []string{"first", "second", "third"} {
		id, err := r.mgr.Send(ctx, emp.ID, prompt)
		if err != nil {
			t.Fatal(err)
		}
		waitUpdate(t, sub, turnEnded(id))
		ids = append(ids, id)
		time.Sleep(5 * time.Millisecond) // distinct start times
	}

	hist, err := r.mgr.History(ctx, emp.ID, 10, time.Time{})
	if err != nil || len(hist) != 3 {
		t.Fatalf("history = %+v, %v; want 3 turns", hist, err)
	}
	if hist[0].ID != ids[2] || hist[2].ID != ids[0] {
		t.Fatalf("history must be newest first, got %s, %s, %s", hist[0].Prompt, hist[1].Prompt, hist[2].Prompt)
	}
	for _, turn := range hist {
		if turn.State != store.TurnCompleted || turn.EndedAt.IsZero() || turn.InputTokens == 0 || turn.CostUSD == 0 {
			t.Errorf("turn %s = %+v, want completed with tokens and cost", turn.Prompt, turn)
		}
	}
	events, err := r.mgr.TurnEvents(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var text string
	for _, e := range events {
		kinds = append(kinds, string(e.Kind))
		if e.Kind == provider.EventText {
			text += e.Event.Text
		}
	}
	// The two text pieces "po" and "ng" are one stored entry.
	if got := strings.Join(kinds, ","); got != "session,text,limits,done" {
		t.Fatalf("stored events = %s, want session,text,limits,done (text pieces joined)", got)
	}
	if text != "pong" {
		t.Fatalf("stored text = %q", text)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Fatal("stored events must keep their order")
		}
	}

	// Paging back through history.
	page, _ := r.mgr.History(ctx, emp.ID, 2, time.Time{})
	older, _ := r.mgr.History(ctx, emp.ID, 2, page[len(page)-1].StartedAt)
	if len(page) != 2 || len(older) != 1 || older[0].ID != ids[0] {
		t.Fatalf("paging gave %d then %d turns", len(page), len(older))
	}

	// A canceled turn is in the history too, as canceled.
	slow, _ := r.worker(t, "Bruno", providertest.Sleep)
	sub2, cancel2 := r.mgr.Subscribe(slow.ID)
	defer cancel2()
	id, _ := r.mgr.Send(ctx, slow.ID, "wait forever")
	waitUpdate(t, sub2, func(u Update) bool { return u.Kind == UpdateTurnStarted })
	time.Sleep(300 * time.Millisecond)
	if err := r.mgr.CancelTurn(slow.ID); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, sub2, turnEnded(id))
	h, _ := r.mgr.History(ctx, slow.ID, 10, time.Time{})
	if len(h) != 1 || h[0].State != store.TurnCanceled {
		t.Fatalf("history of the canceled turn = %+v", h)
	}
	ev, _ := r.mgr.TurnEvents(ctx, id)
	if len(ev) == 0 || ev[len(ev)-1].Kind != provider.EventDone || ev[len(ev)-1].Event.Done.Reason != provider.DoneCanceled {
		t.Fatalf("a canceled turn ends with a canceled done event, got %+v", ev)
	}
}

func TestCancelIsImmediate(t *testing.T) {
	r := newRig(t, Options{})
	emp, p := r.worker(t, "Atlas", providertest.Tree)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()

	id, err := r.mgr.Send(context.Background(), emp.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	leader, grandchild := readPids(t, p.ExtraEnv[providertest.EnvFakePidFile])
	if !providertest.Alive(leader) || !providertest.Alive(grandchild) {
		t.Fatal("both processes must be running before the cancel")
	}
	start := time.Now()
	if err := r.mgr.CancelTurn(emp.ID); err != nil {
		t.Fatal(err)
	}
	u := waitUpdate(t, sub, turnEnded(id))
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the cancel took %s", took)
	}
	if u.State != store.TurnCanceled {
		t.Fatalf("turn state = %q, want canceled", u.State)
	}
	waitGone(t, leader, grandchild)
	if err := r.mgr.CancelTurn(emp.ID); err == nil {
		t.Fatal("canceling with nothing running must say so")
	}
	// The employee works again afterwards.
	if st := r.mgr.Status(emp.ID); st.Working {
		t.Fatalf("status = %+v", st)
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	r := newRig(t, Options{SubscriberBuffer: 512}) // the Chatty program sends well over 1500 events
	ctx := context.Background()
	emp, _ := r.worker(t, "Atlas", providertest.Chatty)

	slow, cancelSlow := r.mgr.Subscribe(emp.ID) // never read
	defer cancelSlow()
	fast, cancelFast := r.mgr.Subscribe(emp.ID)
	defer cancelFast()

	id, err := r.mgr.Send(ctx, emp.ID, "talk a lot")
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	deadline := time.After(30 * time.Second)
loop:
	for {
		select {
		case u, ok := <-fast:
			if !ok {
				t.Fatal("the fast viewer must not be disconnected")
			}
			seen++
			if u.Kind == UpdateTurnEnded && u.TurnID == id {
				break loop
			}
		case <-deadline:
			t.Fatal("a viewer that never reads stopped the turn")
		}
	}
	if seen < 1500 {
		t.Fatalf("the fast viewer saw %d updates, want at least the 1500 text events", seen)
	}
	n := 0
	for range slow { // what was buffered, then the channel is closed: it was disconnected
		n++
	}
	if n > 512 {
		t.Fatalf("the slow viewer held %d updates, more than its buffer", n)
	}
	// Nothing was lost in the history.
	turns, _ := r.mgr.History(ctx, emp.ID, 1, time.Time{})
	events, _ := r.mgr.TurnEvents(ctx, turns[0].ID)
	var text string
	for _, e := range events {
		text += e.Event.Text
	}
	if turns[0].State != store.TurnCompleted || text != strings.Repeat("x", 1500) {
		t.Fatalf("turn %s with %d characters stored, want completed with all 1500", turns[0].State, len(text))
	}
}

func TestParallelEmployeesDoNotBlockEachOther(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	stuck, _ := r.worker(t, "Stuck", providertest.Sleep)
	b, _ := r.worker(t, "Bruno", providertest.Hello)
	c, _ := r.worker(t, "Carla", providertest.Hello)
	all, cancel := r.mgr.Subscribe("")
	defer cancel()

	stuckTurn, err := r.mgr.Send(ctx, stuck.ID, "never ends")
	if err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, all, func(u Update) bool { return u.Kind == UpdateTurnStarted && u.TurnID == stuckTurn })

	bt, _ := r.mgr.Send(ctx, b.ID, "hi")
	ct, _ := r.mgr.Send(ctx, c.ID, "hi")
	got := map[string]string{}
	for len(got) < 2 {
		u := waitUpdate(t, all, func(u Update) bool { return u.Kind == UpdateTurnEnded })
		got[u.TurnID] = u.State
	}
	if got[bt] != store.TurnCompleted || got[ct] != store.TurnCompleted {
		t.Fatalf("turns of the others = %v, want both completed while %s is still stuck", got, stuck.Name)
	}
	if st := r.mgr.Status(stuck.ID); !st.Working {
		t.Fatal("the stuck employee should still be working")
	}
	if _, err := r.mgr.Send(ctx, stuck.ID, "again"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a busy employee takes no second turn: %v", err)
	}
	if err := r.mgr.CancelTurn(stuck.ID); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, all, turnEnded(stuckTurn))
}

func TestConcurrencyLimitQueues(t *testing.T) {
	r := newRig(t, Options{MaxConcurrentTurns: 2})
	ctx := context.Background()
	all, cancel := r.mgr.Subscribe("")
	defer cancel()

	var turns []string
	for _, name := range []string{"A1", "B2", "C3", "D4"} {
		emp, _ := r.worker(t, name, providertest.Slow)
		id, err := r.mgr.Send(ctx, emp.ID, "work")
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, id)
	}
	running, peak, ended := 0, 0, 0
	var order []string // "start <i>" and "end <i>", in the order they were published
	index := map[string]int{}
	for i, id := range turns {
		index[id] = i
	}
	for ended < 4 {
		u := waitUpdate(t, all, func(u Update) bool { return u.Kind == UpdateTurnStarted || u.Kind == UpdateTurnEnded })
		switch u.Kind {
		case UpdateTurnStarted:
			running++
			order = append(order, "start "+itoa(index[u.TurnID]))
			peak = max(peak, running)
		case UpdateTurnEnded:
			running--
			ended++
			order = append(order, "end "+itoa(index[u.TurnID]))
			if u.State != store.TurnCompleted {
				t.Errorf("turn %s ended as %s: queued turns must wait, not fail", u.TurnID, u.State)
			}
		}
	}
	if peak != 2 {
		t.Fatalf("at most 2 turns may run at once and the limit must be reached, peak was %d (%v)", peak, order)
	}
	// The first two to be sent are the first two to run; the other two wait
	// until one of them has finished. (The order between the last two is not
	// asserted: when both slots free up together, which "started" update is
	// published first is a matter of microseconds. That they are served in
	// arrival order is proved by TestFifoSemServesInArrivalOrder.)
	if first := order[0] + "," + order[1]; first != "start 0,start 1" && first != "start 1,start 0" {
		t.Fatalf("the first two turns sent must run first, got %v", order)
	}
	if order[2][:3] != "end" {
		t.Fatalf("a queued turn started before a running one ended: %v", order)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestQueuedTurnCanBeCanceledBeforeItStarts(t *testing.T) {
	r := newRig(t, Options{MaxConcurrentTurns: 1})
	ctx := context.Background()
	first, _ := r.worker(t, "First", providertest.Sleep)
	second, _ := r.worker(t, "Second", providertest.Hello)
	all, cancel := r.mgr.Subscribe("")
	defer cancel()

	t1, _ := r.mgr.Send(ctx, first.ID, "hog the slot")
	waitUpdate(t, all, func(u Update) bool { return u.Kind == UpdateTurnStarted && u.TurnID == t1 })
	t2, _ := r.mgr.Send(ctx, second.ID, "wait in line")
	waitUpdate(t, all, func(u Update) bool { return u.Kind == UpdateTurnQueued && u.TurnID == t2 })

	if err := r.mgr.CancelTurn(second.ID); err != nil {
		t.Fatal(err)
	}
	u := waitUpdate(t, all, turnEnded(t2))
	if u.State != store.TurnCanceled {
		t.Fatalf("a queued turn that is canceled ends as canceled, got %s", u.State)
	}
	events, _ := r.mgr.TurnEvents(ctx, t2)
	if len(events) != 0 {
		t.Fatalf("a turn that never started has no events, got %v", events)
	}
	_ = r.mgr.CancelTurn(first.ID)
	waitUpdate(t, all, turnEnded(t1))
}

func TestPausedEmployeeTakesNoTurns(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	emp, _ := r.worker(t, "Atlas", providertest.Sleep)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()
	id, _ := r.mgr.Send(ctx, emp.ID, "work")
	waitUpdate(t, sub, func(u Update) bool { return u.Kind == UpdateTurnStarted })

	if err := r.mgr.Pause(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	if u := waitUpdate(t, sub, turnEnded(id)); u.State != store.TurnCanceled {
		t.Fatalf("pausing stops the work in progress, turn is %s", u.State)
	}
	if _, err := r.mgr.Send(ctx, emp.ID, "more"); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("a paused employee takes no turns: %v", err)
	}
	if err := r.mgr.Resume(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.mgr.Send(ctx, emp.ID, "more"); err != nil {
		t.Fatalf("after resuming it works again: %v", err)
	}
	_ = r.mgr.CancelTurn(emp.ID)
}

func readPids(t *testing.T, path string) (leader, grandchild int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			f := strings.Fields(string(b))
			if len(f) == 2 {
				return atoi(f[0]), atoi(f[1])
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the fake CLI never wrote its PIDs to %s", path)
	return 0, 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func waitGone(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, p := range pids {
			alive = alive || providertest.Alive(p)
		}
		if !alive {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("processes %v are still running", pids)
}
