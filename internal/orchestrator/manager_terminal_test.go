//go:build !windows

package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
)

// terminalWorker is an employee whose profile runs the fake interactive UI.
func (r *rig) terminalWorker(t *testing.T, name string, sc providertest.Scenario) (Employee, provider.Profile) {
	t.Helper()
	p := r.profile(t, name, sc, provider.ModeTerminal)
	return r.employee(t, name, p), p
}

// termSeen waits until the terminal output contains want.
func termSeen(t *testing.T, term provider.Terminal, want string) string {
	t.Helper()
	replay, live, cancel := term.Subscribe()
	defer cancel()
	got := string(replay)
	deadline := time.After(15 * time.Second)
	for !strings.Contains(got, want) {
		select {
		case chunk, ok := <-live:
			if !ok {
				t.Fatalf("the terminal closed before %q appeared; output: %q", want, got)
			}
			got += string(chunk)
		case <-deadline:
			t.Fatalf("%q never appeared; output: %q", want, got)
		}
	}
	return got
}

func TestSessionKeepsRunningWhenSubscribersLeave(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()

	// A terminal session: the viewer subscribes to updates and leaves.
	emp, _ := r.terminalWorker(t, "Atlas", providertest.TUI)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	term, err := r.mgr.StartTerminal(ctx, emp.ID, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	termSeen(t, term, "FAKE-TUI ready")
	waitUpdate(t, sub, func(u Update) bool { return u.Kind == UpdateSession && u.State == SessionRunning })
	cancel() // the window closes
	time.Sleep(500 * time.Millisecond)
	if st := r.mgr.Status(emp.ID); !st.TerminalRunning {
		t.Fatal("the program must keep running when every viewer has left")
	}
	if again, err := r.mgr.StartTerminal(ctx, emp.ID, 50, 200); err != nil || again == nil {
		t.Fatalf("starting an already running terminal returns it: %v", err)
	}
	termSeen(t, term, "ENV:TERM=") // a viewer that joins later gets the replay

	// A structured turn: the viewer leaves mid-turn; the turn finishes and is stored.
	worker, _ := r.worker(t, "Bruno", providertest.Slow)
	sub2, cancel2 := r.mgr.Subscribe(worker.ID)
	id, err := r.mgr.Send(ctx, worker.ID, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, sub2, func(u Update) bool { return u.Kind == UpdateTurnStarted })
	cancel2()
	deadline := time.Now().Add(15 * time.Second)
	for {
		turn, err := r.st.Turn(ctx, id)
		if err == nil && turn.State == store.TurnCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn did not finish after the viewer left: %+v, %v", turn, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestTerminalSessionSurvivesWithoutViewers(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	emp, _ := r.terminalWorker(t, "Atlas", providertest.TUI)
	if _, err := r.mgr.Terminal(emp.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("before starting: %v, want ErrNotRunning", err)
	}
	if _, err := r.mgr.StartTerminal(ctx, emp.ID, 0, 0); err != nil { // default size
		t.Fatal(err)
	}
	// Nobody subscribes to anything for a while.
	time.Sleep(600 * time.Millisecond)
	term, err := r.mgr.Terminal(emp.ID)
	if err != nil {
		t.Fatalf("the terminal must still be there: %v", err)
	}
	termSeen(t, term, "FAKE-TUI ready")

	// A prompt sent to a terminal employee is typed into the program.
	if id, err := r.mgr.Send(ctx, emp.ID, "hello"); err != nil || id != "" {
		t.Fatalf("Send = %q, %v; a terminal employee has no turns, the prompt is typed", id, err)
	}
	termSeen(t, term, "you said: hello")

	// A structured employee cannot start a terminal.
	worker, _ := r.worker(t, "Bruno", providertest.Hello)
	if _, err := r.mgr.StartTerminal(ctx, worker.ID, 24, 80); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("StartTerminal on a structured employee = %v, want ErrNotTerminal", err)
	}

	// Stopping it on purpose: it ends and does not come back.
	if err := r.mgr.StopTerminal(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := r.mgr.Terminal(emp.ID); return errors.Is(err, ErrNotRunning) })
	waitFor(t, func() bool { row, _ := r.st.Employee(ctx, emp.ID); return row.RunState == store.RunStopped })
	if _, err := r.mgr.Send(ctx, emp.ID, "anyone?"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("typing into a stopped program = %v, want ErrNotRunning", err)
	}
}

func TestRestartPolicyBacksOff(t *testing.T) {
	r := newRig(t, Options{Restart: RestartPolicy{
		Delays:      []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond},
		StableAfter: time.Hour,
	}})
	ctx := context.Background()
	emp, p := r.terminalWorker(t, "Flaky", providertest.Crash)
	sub, cancel := r.mgr.Subscribe(emp.ID)
	defer cancel()

	start := time.Now()
	if _, err := r.mgr.StartTerminal(ctx, emp.ID, 24, 80); err != nil {
		t.Fatal(err)
	}
	var states []string
	var last Update
	for last.State != SessionCrashed {
		last = waitUpdate(t, sub, func(u Update) bool { return u.Kind == UpdateSession })
		states = append(states, last.State)
	}
	if took := time.Since(start); took < 250*time.Millisecond {
		t.Fatalf("the restarts took %s, less than the sum of the delays (280 ms): back-off was not applied", took)
	}
	restarting := 0
	for _, s := range states {
		if s == SessionRestarting {
			restarting++
		}
	}
	if restarting != 3 {
		t.Fatalf("states = %v, want exactly 3 restarts", states)
	}
	if !strings.Contains(last.Detail, "giving up") || !strings.Contains(last.Detail, "3 times") {
		t.Fatalf("the final message must say it gave up and how often it tried: %q", last.Detail)
	}
	launches, _ := os.ReadFile(p.ExtraEnv[providertest.EnvFakePidFile])
	if n := strings.Count(string(launches), "launch"); n != 4 {
		t.Fatalf("the program was launched %d times, want 1 + 3 restarts", n)
	}
	waitFor(t, func() bool { row, _ := r.st.Employee(ctx, emp.ID); return row.RunState == store.RunStopped })
	log, _ := r.svc.Audit(ctx, emp.ID)
	var crashed bool
	for _, a := range log {
		crashed = crashed || a.Action == "session crashed"
	}
	if !crashed {
		t.Fatalf("giving up must be in the audit log: %+v", log)
	}
	// Once given up it stays down.
	time.Sleep(400 * time.Millisecond)
	again, _ := os.ReadFile(p.ExtraEnv[providertest.EnvFakePidFile])
	if strings.Count(string(again), "launch") != 4 {
		t.Fatal("a program that was given up on must not be launched again")
	}
}

func TestSessionResumesAfterDaemonRestart(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	term1, _ := r.terminalWorker(t, "Atlas", providertest.TUI)
	term2, _ := r.terminalWorker(t, "Bruno", providertest.TUI)
	structured, _ := r.worker(t, "Carla", providertest.Hello)

	for _, e := range []Employee{term1, term2} {
		term, err := r.mgr.StartTerminal(ctx, e.ID, 30, 100)
		if err != nil {
			t.Fatal(err)
		}
		termSeen(t, term, "ENV:TERM=")
	}
	// Bruno's terminal is stopped on purpose before the restart: it must stay stopped.
	if err := r.mgr.StopTerminal(ctx, term2.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { row, _ := r.st.Employee(ctx, term2.ID); return row.RunState == store.RunStopped })

	// The provider session ID was captured, and a turn is in flight.
	var id string
	waitFor(t, func() bool { row, _ := r.st.Employee(ctx, term1.ID); id = row.ProviderSessionID; return id != "" })
	inflight := store.TurnRow{ID: "turn-inflight", EmployeeID: structured.ID, Prompt: "was running", State: store.TurnRunning, StartedAt: time.Now()}
	if err := r.st.BeginTurn(ctx, inflight); err != nil {
		t.Fatal(err)
	}

	// The daemon stops cleanly...
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.mgr.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := r.st.Employee(ctx, term1.ID); row.RunState != store.RunRunning {
		t.Fatalf("run state after a clean shutdown = %q: a running terminal must be remembered as running", row.RunState)
	}
	if _, err := r.mgr.Send(ctx, structured.ID, "late"); !errors.Is(err, ErrClosed) {
		t.Fatalf("a shut down manager refuses work: %v", err)
	}

	// ...and a new daemon starts.
	mgr2 := newManagerFor(t, r.env, Options{})
	rec, err := mgr2.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Interrupted != 1 {
		t.Fatalf("interrupted turns = %d, want 1", rec.Interrupted)
	}
	if len(rec.Resumed) != 1 || rec.Resumed[0] != term1.ID || len(rec.Failed) != 0 {
		t.Fatalf("recovered = %+v, want only Atlas resumed (Bruno was stopped on purpose)", rec)
	}
	term, err := mgr2.Terminal(term1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out := termSeen(t, term, "ENV:TERM="); !strings.Contains(out, "ARGS:--resume "+id) {
		t.Fatalf("the program must be relaunched resuming the provider session %s:\n%s", id, out)
	}
	turn, err := r.st.Turn(ctx, "turn-inflight")
	if err != nil || turn.State != store.TurnInterrupted || turn.EndedAt.IsZero() {
		t.Fatalf("the turn that was running = %+v, %v; want interrupted", turn, err)
	}
	if _, err := mgr2.Terminal(term2.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Bruno's terminal was stopped on purpose and must not come back: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a condition")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
