package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"aotus/internal/provider"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// Errors returned by the manager. Use errors.Is.
var (
	// ErrBusy means the employee is already working on a turn.
	ErrBusy = errors.New("the employee is already working on a turn")
	// ErrPaused means the employee is paused.
	ErrPaused = errors.New("the employee is paused")
	// ErrNotTerminal means the employee's profile does not run in terminal mode.
	ErrNotTerminal = errors.New("the employee does not run in terminal mode")
	// ErrNotRunning means the employee's terminal program is not running.
	ErrNotRunning = errors.New("the employee's terminal program is not running")
	// ErrNoProvider means no provider is registered for the profile's kind.
	ErrNoProvider = errors.New("no provider for this kind of profile")
	// ErrClosed means the manager was shut down.
	ErrClosed = errors.New("the manager is shut down")
)

// RestartPolicy says how a terminal session that crashed is brought back.
type RestartPolicy struct {
	// Delays are the waits before each successive restart. When they run out
	// the session is given up on, with a clear error.
	Delays []time.Duration
	// StableAfter is how long a session must run before the count of restarts
	// starts over: a program that crashes every few seconds is given up on, one
	// that crashes once a day is not.
	StableAfter time.Duration
}

// Options configure a Manager. Zero values mean the defaults.
type Options struct {
	// MaxConcurrentTurns limits how many turns run at once across all
	// employees; the rest wait in line (default 4).
	MaxConcurrentTurns int
	// SubscriberBuffer is how many updates a subscriber may fall behind by
	// before it is disconnected (default 256).
	SubscriberBuffer int
	Restart          RestartPolicy
	// TerminalRows and TerminalCols are the size used when the terminal is
	// started without a viewer, for example after a daemon restart.
	TerminalRows, TerminalCols uint16
}

func (o Options) withDefaults() Options {
	if o.MaxConcurrentTurns <= 0 {
		o.MaxConcurrentTurns = 4
	}
	if o.SubscriberBuffer <= 0 {
		o.SubscriberBuffer = 256
	}
	if len(o.Restart.Delays) == 0 {
		o.Restart.Delays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}
	}
	if o.Restart.StableAfter <= 0 {
		o.Restart.StableAfter = 2 * time.Minute
	}
	if o.TerminalRows == 0 {
		o.TerminalRows = 24
	}
	if o.TerminalCols == 0 {
		o.TerminalCols = 80
	}
	return o
}

// Manager keeps the employees' sessions alive in the background, independent of
// any client. It starts turns, streams their events to whoever is watching,
// stores the history, and brings terminal sessions back after a crash or a
// restart of the daemon.
type Manager struct {
	svc       *Service
	st        *store.Store
	ws        *workspace.Manager
	providers map[provider.Kind]provider.Provider
	opts      Options

	hub *hub
	sem *fifoSem

	mu     sync.Mutex
	rts    map[string]*runtime
	closed bool
	wg     sync.WaitGroup
}

// NewManager returns a manager. providers maps each kind of profile to the
// provider that runs it.
func NewManager(svc *Service, providers map[provider.Kind]provider.Provider, opts Options) *Manager {
	opts = opts.withDefaults()
	return &Manager{
		svc: svc, st: svc.st, ws: svc.ws, providers: providers, opts: opts,
		hub: newHub(opts.SubscriberBuffer), sem: newFifoSem(opts.MaxConcurrentTurns),
		rts: map[string]*runtime{},
	}
}

// Service is the employee and profile service the manager works with.
func (m *Manager) Service() *Service { return m.svc }

// Subscribe delivers the updates of one employee, or of all with "". See hub.
func (m *Manager) Subscribe(employeeID string) (<-chan Update, func()) {
	return m.hub.subscribe(employeeID)
}

// runtime is the live state of one employee.
type runtime struct {
	m  *Manager
	id string

	mu          sync.Mutex
	sess        provider.Session
	busy        bool
	turnID      string
	turnCancel  context.CancelFunc
	stopping    bool // the terminal is being stopped on purpose
	keepRunning bool // ... but it should come back after a daemon restart
	shutdown    bool // the daemon is shutting down
	restarts    int
	lastLaunch  time.Time
	rows, cols  uint16
	timer       *time.Timer
	watching    bool
}

func (m *Manager) runtime(id string) (*runtime, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	rt := m.rts[id]
	if rt == nil {
		rt = &runtime{m: m, id: id}
		m.rts[id] = rt
	}
	return rt, nil
}

// session returns the employee's provider session, creating it on first use
// from the employee, its profile and its workspace folder.
func (rt *runtime) session(ctx context.Context) (provider.Session, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sess != nil {
		return rt.sess, nil
	}
	m := rt.m
	emp, err := m.svc.Employee(ctx, rt.id)
	if err != nil {
		return nil, err
	}
	profile, err := m.svc.Profile(ctx, emp.ProfileID)
	if err != nil {
		return nil, err
	}
	prov, ok := m.providers[profile.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoProvider, profile.Kind)
	}
	dir, err := m.ws.Create(emp.Slug)
	if err != nil {
		return nil, err
	}
	env, err := m.ws.Env(emp.Slug)
	if err != nil {
		return nil, err
	}
	row, err := m.st.Employee(ctx, rt.id)
	if err != nil {
		return nil, err
	}
	sess, err := prov.Start(ctx, provider.SessionRequest{
		Profile: profile, Dir: dir, SystemPrompt: emp.SystemPrompt, ResumeID: row.ProviderSessionID,
		PermissionMode: emp.PermissionMode, AllowedTools: emp.AllowedTools, ExtraEnv: env,
	})
	if err != nil {
		return nil, err
	}
	rt.sess = sess
	return sess, nil
}

func (m *Manager) active(ctx context.Context, id string) (Employee, error) {
	emp, err := m.svc.Employee(ctx, id)
	if err != nil {
		return Employee{}, err
	}
	if emp.State == StatePaused {
		return Employee{}, fmt.Errorf("%w: %s", ErrPaused, emp.Name)
	}
	return emp, nil
}

// Send gives the employee a prompt. For employees that run structured turns it
// starts a turn and returns at once with its ID; the events arrive through
// Subscribe and are stored in the history. For employees in terminal mode it
// types the prompt into the running program.
func (m *Manager) Send(ctx context.Context, employeeID, prompt string) (string, error) {
	if _, err := m.active(ctx, employeeID); err != nil {
		return "", err
	}
	rt, err := m.runtime(employeeID)
	if err != nil {
		return "", err
	}
	sess, err := rt.session(ctx)
	if err != nil {
		return "", err
	}
	if ts, ok := sess.(provider.TerminalSession); ok {
		if ts.Terminal() == nil {
			return "", ErrNotRunning
		}
		return "", ts.Send(ctx, prompt)
	}

	rt.mu.Lock()
	if rt.busy {
		rt.mu.Unlock()
		return "", ErrBusy
	}
	rt.busy = true
	rt.mu.Unlock()

	turn := store.TurnRow{ID: newTurnID(), EmployeeID: employeeID, Prompt: prompt, State: store.TurnQueued, StartedAt: time.Now()}
	if err := m.st.BeginTurn(ctx, turn); err != nil {
		rt.release()
		return "", err
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	rt.mu.Lock()
	rt.turnID, rt.turnCancel = turn.ID, cancel
	rt.mu.Unlock()
	m.hub.publish(Update{Kind: UpdateTurnQueued, EmployeeID: employeeID, TurnID: turn.ID})

	m.wg.Add(1)
	//nolint:gosec // a turn deliberately outlives the request that started it; CancelTurn and Shutdown end it
	go rt.runTurn(turnCtx, cancel, sess, turn)
	return turn.ID, nil
}

func (rt *runtime) release() {
	rt.mu.Lock()
	rt.busy, rt.turnID, rt.turnCancel = false, "", nil
	rt.mu.Unlock()
}

// runTurn waits for a slot, runs the turn, and records everything.
func (rt *runtime) runTurn(ctx context.Context, cancel context.CancelFunc, sess provider.Session, turn store.TurnRow) {
	m := rt.m
	defer m.wg.Done()
	defer cancel()
	end := func(state, errText string, done *provider.Done) {
		t := store.TurnRow{ID: turn.ID, State: state, Error: errText, EndedAt: time.Now()}
		if done != nil {
			t.CostUSD, t.InputTokens, t.OutputTokens = done.CostUSD, done.InputTokens, done.OutputTokens
		}
		bg, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = m.st.EndTurn(bg, t)
		if id := sess.ID(); id != "" {
			_ = m.st.SetProviderSession(bg, rt.id, id)
		}
		rt.release() // free before anyone learns the turn ended
		m.hub.publish(Update{Kind: UpdateTurnEnded, EmployeeID: rt.id, TurnID: turn.ID, State: state, Detail: errText})
	}

	if err := m.sem.Acquire(ctx); err != nil { // canceled while waiting in line
		end(rt.endState(store.TurnCanceled), "", nil)
		return
	}
	defer m.sem.Release()

	_ = m.st.SetTurnState(context.Background(), turn.ID, store.TurnRunning)
	m.hub.publish(Update{Kind: UpdateTurnStarted, EmployeeID: rt.id, TurnID: turn.ID})

	if err := sess.Send(ctx, turn.Prompt); err != nil {
		end(rt.endState(store.TurnFailed), err.Error(), nil)
		return
	}
	w := &turnWriter{st: m.st, turnID: turn.ID}
	var lastError string
	for ev := range sess.Events() {
		ev := ev
		if ev.Kind == provider.EventError {
			lastError = ev.Text
		}
		w.add(ev)
		m.hub.publish(Update{Kind: UpdateEvent, EmployeeID: rt.id, TurnID: turn.ID, Event: &ev})
		if ev.Kind != provider.EventDone {
			continue
		}
		w.flush()
		switch ev.Done.Reason {
		case provider.DoneCompleted:
			end(store.TurnCompleted, "", ev.Done)
		case provider.DoneCanceled:
			end(rt.endState(store.TurnCanceled), "", ev.Done)
		default:
			end(store.TurnFailed, lastError, ev.Done)
		}
		return
	}
	// The event stream closed without a done event: the session was closed.
	w.flush()
	end(store.TurnInterrupted, "the session was closed while the turn was running", nil)
}

// endState turns "canceled" into "interrupted" when the daemon itself is
// stopping, so that history can tell a user's cancel from a shutdown.
func (rt *runtime) endState(state string) string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.shutdown && state == store.TurnCanceled {
		return store.TurnInterrupted
	}
	return state
}

// CancelTurn ends the employee's running turn at once, or removes it from the
// queue if it has not started.
func (m *Manager) CancelTurn(employeeID string) error {
	rt, err := m.runtime(employeeID)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	cancel, busy := rt.turnCancel, rt.busy
	rt.mu.Unlock()
	if !busy || cancel == nil {
		return provider.ErrNoTurn
	}
	cancel() // stops the queue wait, or the process tree through the turn's context
	return nil
}

// Interrupt asks the employee's running turn to stop politely (like Ctrl+C);
// for a terminal session it types Ctrl+C.
func (m *Manager) Interrupt(employeeID string) error {
	rt, err := m.runtime(employeeID)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	sess := rt.sess
	rt.mu.Unlock()
	if sess == nil {
		return provider.ErrNoTurn
	}
	return sess.Interrupt()
}

// Status describes what an employee is doing.
type Status struct {
	Mode    provider.Mode
	Working bool   // a turn is queued or running
	TurnID  string // that turn
	// TerminalRunning is true when the employee's terminal program runs.
	TerminalRunning bool
}

// Status reports what an employee is doing right now.
func (m *Manager) Status(employeeID string) Status {
	m.mu.Lock()
	rt := m.rts[employeeID]
	m.mu.Unlock()
	if rt == nil {
		return Status{}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st := Status{Working: rt.busy, TurnID: rt.turnID}
	if rt.sess != nil {
		st.Mode = rt.sess.Mode()
		if ts, ok := rt.sess.(provider.TerminalSession); ok {
			st.TerminalRunning = ts.Terminal() != nil
		}
	}
	return st
}

// Pause stops the employee's work and keeps it from starting more.
func (m *Manager) Pause(ctx context.Context, employeeID string) error {
	if err := m.svc.Pause(ctx, employeeID); err != nil {
		return err
	}
	m.stop(ctx, employeeID, false)
	return nil
}

// Resume lets a paused employee work again. A terminal program is not started
// by this; the user starts it when they want to.
func (m *Manager) Resume(ctx context.Context, employeeID string) error {
	return m.svc.Resume(ctx, employeeID)
}

// Delete retires an employee: its work stops, its session is closed and its
// folder goes to the trash.
func (m *Manager) Delete(ctx context.Context, employeeID string) error {
	m.stop(ctx, employeeID, false)
	m.mu.Lock()
	rt := m.rts[employeeID]
	delete(m.rts, employeeID)
	m.mu.Unlock()
	if rt != nil {
		rt.mu.Lock()
		sess := rt.sess
		rt.sess = nil
		rt.mu.Unlock()
		if sess != nil {
			_ = sess.Close()
		}
	}
	return m.svc.DeleteEmployee(ctx, employeeID)
}

func newTurnID() string { return "turn-" + newID()[4:] }
