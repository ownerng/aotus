package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"aotus/internal/proc"
)

// ErrNotLaunched means the terminal program is not running.
var ErrNotLaunched = errors.New("the terminal program is not running; launch it first")

// terminalEnv completes a profile's environment for an interactive program:
// without a TERM the daemon, started at login, would give it none.
func terminalEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		if len(kv) >= 5 && (kv[:5] == "TERM=" || (len(kv) >= 10 && kv[:10] == "COLORTERM=")) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// terminalLauncher is what differs between CLIs: the command line of the
// interactive program.
type terminalLauncher interface {
	// New returns the arguments for a fresh session. newID is the session ID we
	// chose, for CLIs that let us choose one ("" otherwise).
	New(req SessionRequest, newID string) []string
	// Resume returns the arguments to continue a session. id is "" when the ID
	// is not known and the CLI must pick the most recent session of the
	// working folder, which is the employee's own.
	Resume(req SessionRequest, id string) []string
}

// newTerminalSession creates a session that hosts a CLI's interactive UI.
func newTerminalSession(req SessionRequest, l terminalLauncher, binary string, env []string, canChooseID bool) TerminalSession {
	return &terminalSession{
		req: req, l: l, binary: binary, env: terminalEnv(env), canChooseID: canChooseID,
		sessionID: req.ResumeID, ranBefore: req.ResumeID != "",
		events: make(chan Event, 16), closing: make(chan struct{}), done: make(chan struct{}),
	}
}

type terminalSession struct {
	req         SessionRequest
	l           terminalLauncher
	binary      string
	env         []string
	canChooseID bool

	events  chan Event
	closing chan struct{}
	done    chan struct{}

	mu          sync.Mutex
	sessionID   string
	ranBefore   bool
	pty         *proc.PTYProcess
	interrupted atomic.Bool
	closed      bool
	wg          sync.WaitGroup
}

func (s *terminalSession) Mode() Mode { return ModeTerminal }

func (s *terminalSession) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *terminalSession) Events() <-chan Event { return s.events }

func (s *terminalSession) Done() <-chan struct{} { return s.done }

func (s *terminalSession) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.closing:
	}
}

func (s *terminalSession) Launch(_ context.Context, rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if s.pty != nil {
		return ErrTurnActive
	}
	var args []string
	newID := ""
	switch {
	case s.sessionID != "":
		args = s.l.Resume(s.req, s.sessionID)
	case s.ranBefore:
		args = s.l.Resume(s.req, "") // ran before without a known ID
	default:
		if s.canChooseID {
			newID = newUUID()
		}
		args = s.l.New(s.req, newID)
	}
	p, err := proc.StartPTY(context.Background(), proc.PTYSpec{
		Spec: proc.Spec{Path: s.binary, Args: args, Dir: s.req.Dir, Env: s.env},
		Rows: rows, Cols: cols,
	})
	if err != nil {
		return err
	}
	s.pty = p
	s.ranBefore = true
	s.interrupted.Store(false)
	if newID != "" {
		s.sessionID = newID
		s.emit(Event{Kind: EventSession, SessionID: newID})
	}
	s.wg.Add(1)
	go s.watch(p, filepath.Base(s.binary))
	return nil
}

// watch reports the end of the program as the session's done event.
func (s *terminalSession) watch(p *proc.PTYProcess, name string) {
	defer s.wg.Done()
	exit := p.Wait()
	s.mu.Lock()
	s.pty = nil // free for a new Launch before anyone sees the done event
	s.mu.Unlock()
	switch {
	case exit.Canceled || s.interrupted.Load():
		s.emit(Event{Kind: EventDone, Done: &Done{Reason: DoneCanceled}})
	case exit.Code == 0:
		s.emit(Event{Kind: EventDone, Done: &Done{Reason: DoneCompleted}})
	default:
		s.emit(Event{Kind: EventError, Code: CodeCLI, Text: exitMessage(name, exit, nil)})
		s.emit(Event{Kind: EventDone, Done: &Done{Reason: DoneFailed}})
	}
}

func (s *terminalSession) current() *proc.PTYProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pty
}

func (s *terminalSession) Terminal() Terminal {
	if p := s.current(); p != nil {
		return p
	}
	return nil
}

// Send types the prompt and presses Enter in the running program.
func (s *terminalSession) Send(_ context.Context, prompt string) error {
	p := s.current()
	if p == nil {
		return ErrNotLaunched
	}
	_, err := p.Write([]byte(prompt + "\r"))
	return err
}

// Interrupt types Ctrl+C into the terminal.
func (s *terminalSession) Interrupt() error {
	p := s.current()
	if p == nil {
		return ErrNoTurn
	}
	s.interrupted.Store(true)
	return p.Interrupt()
}

// CancelTurn ends the program and everything it started. The session can be
// launched again and resumes the provider session.
func (s *terminalSession) CancelTurn() error {
	p := s.current()
	if p == nil {
		return ErrNoTurn
	}
	p.Cancel()
	return nil
}

func (s *terminalSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closed = true
	p := s.pty
	s.mu.Unlock()

	close(s.closing)
	if p != nil {
		p.Cancel()
	}
	s.wg.Wait()
	close(s.events)
	close(s.done)
	return nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
