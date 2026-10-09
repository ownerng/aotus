package providertest

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"aotus/internal/provider"
)

// Probe gives the contract checks a way to look inside the CLI under test.
type Probe struct {
	// PidFile is where the CLI wrote "leader grandchild" PIDs in the Tree
	// scenario.
	PidFile string
}

// Contract describes how to run the shared checks against one provider.
type Contract struct {
	// New returns a fresh session whose CLI behaves as the scenario says. A
	// real adapter implements it with a fake CLI that speaks its dialect.
	New func(t *testing.T, sc Scenario) (provider.Session, Probe)
}

const wait = 10 * time.Second

// RunContract runs every check. Real adapters call it from their tests with
// their own Contract.
func RunContract(t *testing.T, c Contract) {
	t.Run("events are normalized", func(t *testing.T) { CheckNormalized(t, c) })
	t.Run("resume continues the session", func(t *testing.T) { CheckResume(t, c) })
	t.Run("cancel stops the process tree", func(t *testing.T) { CheckCancelStopsTree(t, c) })
	t.Run("interrupt ends the turn as canceled", func(t *testing.T) { CheckInterrupt(t, c) })
	t.Run("environment never leaks", func(t *testing.T) { CheckNoEnvLeak(t, c) })
	t.Run("stdin is closed", func(t *testing.T) { CheckStdinClosed(t, c) })
	t.Run("one turn at a time", func(t *testing.T) { CheckOneTurnAtATime(t, c) })
	t.Run("close ends the session", func(t *testing.T) { CheckCloseEndsSession(t, c) })
}

func newSession(t *testing.T, c Contract, sc Scenario) (provider.Session, Probe) {
	t.Helper()
	s, probe := c.New(t, sc)
	t.Cleanup(func() { _ = s.Close() })
	return s, probe
}

// turn sends a prompt and collects events up to and including the done event.
func turn(t *testing.T, s provider.Session, prompt string) []provider.Event {
	t.Helper()
	if err := s.Send(context.Background(), prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return collectTurn(t, s)
}

func collectTurn(t *testing.T, s provider.Session) []provider.Event {
	t.Helper()
	var events []provider.Event
	deadline := time.After(wait)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatalf("event stream closed before the turn ended; got %v", events)
			}
			events = append(events, ev)
			if ev.Kind == provider.EventDone {
				return events
			}
		case <-deadline:
			t.Fatalf("turn did not end within %s; events so far: %v", wait, events)
		}
	}
}

func text(events []provider.Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Kind == provider.EventText {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// CheckNormalized: a normal turn and a failing turn both follow the event
// contract.
func CheckNormalized(t *testing.T, c Contract) {
	t.Helper()
	s, _ := newSession(t, c, Hello)
	events := turn(t, s, "ping")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("normal turn violates the contract: %v\n%v", err, events)
	}
	if got := text(events); got != "pong" {
		t.Fatalf("answer = %q, want pong (text deltas must be concatenated in order)", got)
	}
	if last := events[len(events)-1]; last.Done.Reason != provider.DoneCompleted {
		t.Fatalf("done = %+v, want completed", last.Done)
	}
	if s.ID() == "" {
		t.Fatal("the session ID reported by the CLI must be available through ID()")
	}

	f, _ := newSession(t, c, Fail)
	failed := turn(t, f, "ping")
	if err := provider.ValidateTurn(failed); err != nil {
		t.Fatalf("failed turn violates the contract: %v\n%v", err, failed)
	}
	var sawLogin bool
	for _, e := range failed {
		if e.Kind == provider.EventError && e.Code == provider.CodeNeedsLogin {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Fatalf("the login problem must reach the user as a needs_login error; got %v", failed)
	}
	if last := failed[len(failed)-1]; last.Done.Reason != provider.DoneFailed {
		t.Fatalf("done = %+v, want failed", last.Done)
	}
}

// CheckResume: a second turn continues the provider session of the first.
func CheckResume(t *testing.T, c Contract) {
	t.Helper()
	s, _ := newSession(t, c, Hello)
	turn(t, s, "first")
	id := s.ID()
	second := turn(t, s, "second")
	if err := provider.ValidateTurn(second); err != nil {
		t.Fatal(err)
	}
	if got := text(second); got != "resumed:"+id {
		t.Fatalf("second turn answered %q, want resumed:%s: the session ID must be passed to the CLI", got, id)
	}
}

// CheckCancelStopsTree: CancelTurn kills the CLI and everything it started,
// and the turn ends as canceled.
func CheckCancelStopsTree(t *testing.T, c Contract) {
	t.Helper()
	s, probe := newSession(t, c, Tree)
	if err := s.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	leader, grandchild := readPids(t, probe.PidFile)
	if !Alive(leader) || !Alive(grandchild) {
		t.Fatalf("both processes must run before the cancel (leader %d, grandchild %d)", leader, grandchild)
	}
	if err := s.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	events := collectTurn(t, s)
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("canceled turn violates the contract: %v\n%v", err, events)
	}
	if last := events[len(events)-1]; last.Done.Reason != provider.DoneCanceled {
		t.Fatalf("done = %+v, want canceled", last.Done)
	}
	waitDead(t, leader, grandchild)
}

// CheckInterrupt: a polite interrupt ends the turn as canceled and the error
// the CLI prints because of it is not shown as a failure.
func CheckInterrupt(t *testing.T, c Contract) {
	t.Helper()
	s, _ := newSession(t, c, Interruptible)
	if err := s.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// Wait until the CLI is running before interrupting it.
	select {
	case ev := <-s.Events():
		if ev.Kind != provider.EventSession && ev.Kind != provider.EventText {
			t.Fatalf("unexpected first event %+v", ev)
		}
	case <-time.After(wait):
		t.Fatal("the CLI never started")
	}
	time.Sleep(200 * time.Millisecond)
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	events := collectTurn(t, s)
	if last := events[len(events)-1]; last.Done.Reason != provider.DoneCanceled {
		t.Fatalf("done = %+v, want canceled", last.Done)
	}
	for _, e := range events {
		if e.Kind == provider.EventError {
			t.Fatalf("an interrupt requested by the user must not surface as an error: %+v", e)
		}
	}
}

// CheckNoEnvLeak: a secret in the daemon's environment reaches neither the CLI
// nor any event.
func CheckNoEnvLeak(t *testing.T, c Contract) {
	t.Helper()
	const secret = "do-not-leak-this-token-1234"
	t.Setenv("AOTUS_CONTRACT_SECRET", secret)
	s, _ := newSession(t, c, EnvDump)
	events := turn(t, s, "dump")
	var dumped int
	for _, e := range events {
		if strings.Contains(e.Text, secret) || strings.Contains(e.SessionID, secret) {
			t.Fatalf("the daemon's secret reached the CLI or an event: %+v", e)
		}
		if strings.HasPrefix(e.Text, "ENV:") {
			dumped++
		}
	}
	if dumped == 0 {
		t.Fatal("the scenario printed no environment: the check proves nothing")
	}
}

// CheckStdinClosed: the CLI sees the end of its stdin, so it never waits for
// input that will not come.
func CheckStdinClosed(t *testing.T, c Contract) {
	t.Helper()
	s, _ := newSession(t, c, Stdin)
	events := turn(t, s, "hi")
	if got := text(events); got != "stdin-eof" {
		t.Fatalf("answer = %q, want stdin-eof: stdin must be closed after the prompt is delivered", got)
	}
}

// CheckOneTurnAtATime: a second Send during a turn is refused, and the session
// is usable after the first turn is canceled.
func CheckOneTurnAtATime(t *testing.T, c Contract) {
	t.Helper()
	s, _ := newSession(t, c, Sleep)
	if err := s.Send(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "two"); !errors.Is(err, provider.ErrTurnActive) {
		t.Fatalf("second Send = %v, want ErrTurnActive", err)
	}
	if err := s.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	collectTurn(t, s)
	if err := s.CancelTurn(); !errors.Is(err, provider.ErrNoTurn) {
		t.Fatalf("CancelTurn with no turn = %v, want ErrNoTurn", err)
	}
}

// CheckCloseEndsSession: Close stops the running turn, closes the event stream
// and refuses further turns.
func CheckCloseEndsSession(t *testing.T, c Contract) {
	t.Helper()
	s, probe := newSession(t, c, Tree)
	if err := s.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	leader, grandchild := readPids(t, probe.PidFile)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(wait):
		t.Fatal("Done must be closed after Close")
	}
	for range s.Events() { // must terminate: the stream is closed
	}
	waitDead(t, leader, grandchild)
	if err := s.Send(context.Background(), "again"); !errors.Is(err, provider.ErrSessionClosed) {
		t.Fatalf("Send after Close = %v, want ErrSessionClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close must be idempotent, got %v", err)
	}
}

func readPids(t *testing.T, path string) (leader, grandchild int) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if f := strings.Fields(string(b)); len(f) == 2 {
				l, e1 := strconv.Atoi(f[0])
				g, e2 := strconv.Atoi(f[1])
				if e1 == nil && e2 == nil {
					return l, g
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the CLI never recorded its PIDs in %s", path)
	return 0, 0
}

func waitDead(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var running []int
		for _, p := range pids {
			if Alive(p) {
				running = append(running, p)
			}
		}
		if len(running) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes still running after the turn was canceled: %v", running)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
