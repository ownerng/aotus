package provider

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aotus/internal/proc"
)

// InterruptGrace is how long a turn gets to end after Interrupt before the
// whole process tree is killed.
var InterruptGrace = 5 * time.Second

// Command is what a dialect wants run for one turn.
type Command struct {
	Spec proc.Spec
	// Stdin is written to the process and then stdin is closed. Empty means
	// stdin is closed right away (the CLI would otherwise wait for it).
	Stdin string
}

// TurnRequest is the input of Dialect.Command.
type TurnRequest struct {
	Prompt string
	// SessionID is the provider session to continue, "" for a new one.
	SessionID string
	Request   SessionRequest
}

// Dialect is the part of an adapter that differs between CLIs. The shared
// session code does everything else: starting the process through proc,
// streaming, interrupting, cancelling, and closing every turn with exactly one
// done event.
type Dialect interface {
	// Command builds the process to run for a turn.
	Command(t TurnRequest) (Command, error)
	// Parse turns one line of output into zero or more events. It must
	// tolerate lines and fields it does not know.
	Parse(l proc.Line) []Event
	// OnExit is called after the process ended and all lines were parsed. It
	// may classify a failure (for example a login problem found in stderr). It
	// must not emit a done event unless Parse did not.
	OnExit(exit proc.Exit, recent []proc.Line) []Event
}

// NewProcessSession creates a session that runs one supervised process per
// turn using the dialect.
func NewProcessSession(mode Mode, d Dialect, req SessionRequest) Session {
	return &processSession{
		mode:      mode,
		d:         d,
		req:       req,
		sessionID: req.ResumeID,
		events:    make(chan Event, 256),
		closing:   make(chan struct{}),
		done:      make(chan struct{}),
	}
}

type processSession struct {
	mode Mode
	d    Dialect
	req  SessionRequest

	events  chan Event
	closing chan struct{} // closed when Close starts: unblocks emitters
	done    chan struct{} // closed when Close finished

	mu        sync.Mutex
	sessionID string
	turn      *activeTurn
	closed    bool
	wg        sync.WaitGroup
}

type activeTurn struct {
	p    *proc.Process
	stop atomic.Bool // the user or the daemon asked this turn to end
}

func (s *processSession) Mode() Mode { return s.mode }

func (s *processSession) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *processSession) Events() <-chan Event { return s.events }

func (s *processSession) Done() <-chan struct{} { return s.done }

func (s *processSession) Send(ctx context.Context, prompt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.turn != nil {
		return ErrTurnActive
	}
	cmd, err := s.d.Command(TurnRequest{Prompt: prompt, SessionID: s.sessionID, Request: s.req})
	if err != nil {
		return err
	}
	p, err := proc.Start(ctx, cmd.Spec)
	if err != nil {
		return err
	}
	// Always close stdin: CLIs in print mode otherwise wait for input.
	go func(text string) {
		w := p.Stdin()
		if text != "" {
			_, _ = w.Write([]byte(text))
		}
		_ = w.Close()
	}(cmd.Stdin)

	t := &activeTurn{p: p}
	s.turn = t
	s.wg.Add(1)
	go s.pump(t, filepath.Base(cmd.Spec.Path))
	return nil
}

// pump reads a turn's output, normalizes it and makes sure the turn ends with
// exactly one done event.
func (s *processSession) pump(t *activeTurn, name string) {
	defer s.wg.Done()
	var st turnState

	for l := range t.p.Lines() {
		for _, ev := range s.d.Parse(l) {
			s.handle(t, &st, ev)
		}
	}
	exit := t.p.Wait()
	if exit.Canceled {
		t.stop.Store(true)
	}
	for _, ev := range s.d.OnExit(exit, t.p.Recent()) {
		s.handle(t, &st, ev)
	}
	if !st.done {
		s.finish(t, &st, exit, name)
	}

	// The session is free for the next turn before the consumer can see the
	// done event; otherwise a prompt sent right after "done" could be refused.
	s.mu.Lock()
	s.turn = nil
	s.mu.Unlock()
	s.emit(*st.pending)
}

type turnState struct {
	done     bool
	sawError bool
	pending  *Event // the done event, held back until the turn is over
}

// handle applies the shared rules to one event and emits it.
func (s *processSession) handle(t *activeTurn, st *turnState, ev Event) {
	if st.done {
		return // nothing may follow the done event
	}
	switch ev.Kind {
	case EventSession:
		s.mu.Lock()
		s.sessionID = ev.SessionID
		s.mu.Unlock()
	case EventError:
		if t.stop.Load() {
			return // failures caused by our own cancel are not errors
		}
		st.sawError = true
	case EventDone:
		if ev.Done == nil {
			ev.Done = &Done{Reason: DoneCompleted}
		}
		if t.stop.Load() {
			d := *ev.Done
			d.Reason = DoneCanceled
			ev.Done = &d
		}
		st.done = true
		st.pending = &ev // emitted by pump once the turn is over
		return
	}
	s.emit(ev)
}

// finish synthesizes the done event when the CLI did not send one.
func (s *processSession) finish(t *activeTurn, st *turnState, exit proc.Exit, name string) {
	done := &Done{Reason: DoneFailed}
	switch {
	case t.stop.Load():
		done.Reason = DoneCanceled
	case exit.Code == 0 && !st.sawError:
		done.Reason = DoneCompleted
	default:
		if !st.sawError {
			s.emit(Event{Kind: EventError, Code: CodeCLI, Text: exitMessage(name, exit, t.p.Recent())})
		}
	}
	st.pending = &Event{Kind: EventDone, Done: done}
	st.done = true
}

func exitMessage(name string, exit proc.Exit, recent []proc.Line) string {
	msg := fmt.Sprintf("%s ended with exit code %d", name, exit.Code)
	if exit.Signal != "" {
		msg = fmt.Sprintf("%s was ended by signal %s", name, exit.Signal)
	}
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Stream == proc.Stderr && strings.TrimSpace(recent[i].Text) != "" {
			last := strings.TrimSpace(recent[i].Text)
			if len(last) > 300 {
				last = last[:300] + "..."
			}
			return msg + ": " + last
		}
	}
	return msg
}

func (s *processSession) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.closing:
	}
}

func (s *processSession) active() *activeTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turn
}

func (s *processSession) Interrupt() error {
	t := s.active()
	if t == nil {
		return ErrNoTurn
	}
	t.stop.Store(true)
	if err := t.p.Interrupt(); err != nil {
		if errors.Is(err, proc.ErrInterruptUnsupported) {
			t.p.Cancel()
			return nil
		}
		return err
	}
	// A CLI that ignores the interrupt must not hang the turn forever.
	timer := time.AfterFunc(InterruptGrace, t.p.Cancel)
	go func() {
		<-t.p.Done()
		timer.Stop()
	}()
	return nil
}

func (s *processSession) CancelTurn() error {
	t := s.active()
	if t == nil {
		return ErrNoTurn
	}
	t.stop.Store(true)
	t.p.Cancel()
	return nil
}

func (s *processSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closed = true
	t := s.turn
	s.mu.Unlock()

	close(s.closing)
	if t != nil {
		t.stop.Store(true)
		t.p.Cancel()
	}
	s.wg.Wait()
	close(s.events)
	close(s.done)
	return nil
}
