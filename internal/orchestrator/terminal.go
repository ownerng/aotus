package orchestrator

import (
	"context"
	"fmt"
	"time"

	"aotus/internal/provider"
	"aotus/internal/store"
)

// StartTerminal launches the employee's interactive program (the official
// Claude Code or Codex CLI) in a pseudo-terminal that the daemon keeps alive,
// whether or not any window is open. If it is already running it returns the
// running terminal. The size is only used when the program is started now.
func (m *Manager) StartTerminal(ctx context.Context, employeeID string, rows, cols uint16) (provider.Terminal, error) {
	if _, err := m.active(ctx, employeeID); err != nil {
		return nil, err
	}
	rt, err := m.runtime(employeeID)
	if err != nil {
		return nil, err
	}
	sess, err := rt.session(ctx)
	if err != nil {
		return nil, err
	}
	ts, ok := sess.(provider.TerminalSession)
	if !ok {
		return nil, ErrNotTerminal
	}
	rt.mu.Lock()
	if rows == 0 || cols == 0 {
		rows, cols = m.opts.TerminalRows, m.opts.TerminalCols
	}
	rt.rows, rt.cols = rows, cols
	rt.stopping, rt.keepRunning = false, false
	if t := ts.Terminal(); t != nil {
		rt.mu.Unlock()
		return t, nil
	}
	if !rt.watching {
		rt.watching = true
		m.wg.Add(1)
		//nolint:gosec // the watcher lives as long as the session, not as long as one request
		go rt.watchTerminal(ts)
	}
	rt.mu.Unlock()

	if err := rt.launch(ctx, ts); err != nil {
		return nil, err
	}
	return ts.Terminal(), nil
}

// launch starts the program and records that it should come back after a
// restart of the daemon.
func (rt *runtime) launch(ctx context.Context, ts provider.TerminalSession) error {
	rt.mu.Lock()
	rows, cols := rt.rows, rt.cols
	rt.mu.Unlock()
	if err := ts.Launch(ctx, rows, cols); err != nil {
		return err
	}
	rt.mu.Lock()
	rt.lastLaunch = time.Now()
	rt.mu.Unlock()
	_ = rt.m.st.SetRunState(context.Background(), rt.id, store.RunRunning)
	rt.m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionRunning})
	return nil
}

// Terminal returns the employee's running terminal, or ErrNotRunning.
func (m *Manager) Terminal(employeeID string) (provider.Terminal, error) {
	m.mu.Lock()
	rt := m.rts[employeeID]
	m.mu.Unlock()
	if rt == nil {
		return nil, ErrNotRunning
	}
	rt.mu.Lock()
	sess := rt.sess
	rt.mu.Unlock()
	if ts, ok := sess.(provider.TerminalSession); ok {
		if t := ts.Terminal(); t != nil {
			return t, nil
		}
	}
	return nil, ErrNotRunning
}

// StopTerminal ends the employee's terminal program on purpose. It will not be
// restarted, and not brought back after a daemon restart.
func (m *Manager) StopTerminal(ctx context.Context, employeeID string) error {
	m.stop(ctx, employeeID, false)
	return nil
}

// stop ends whatever the employee is running. keep says whether a terminal
// session should be brought back after a daemon restart (true only when the
// daemon itself is shutting down).
func (m *Manager) stop(ctx context.Context, employeeID string, keep bool) {
	m.mu.Lock()
	rt := m.rts[employeeID]
	m.mu.Unlock()
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.stopping, rt.keepRunning = true, keep
	cancel := rt.turnCancel
	sess := rt.sess
	if rt.timer != nil {
		rt.timer.Stop()
		rt.timer = nil
	}
	rt.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ts, ok := sess.(provider.TerminalSession); ok {
		if ts.Terminal() != nil {
			_ = ts.CancelTurn()
		} else if !keep {
			_ = m.st.SetRunState(ctx, employeeID, store.RunStopped)
			m.hub.publish(Update{Kind: UpdateSession, EmployeeID: employeeID, State: SessionStopped})
		}
	}
}

// watchTerminal follows a terminal session's events for as long as it exists:
// it remembers the provider session ID and decides what an ended program means.
func (rt *runtime) watchTerminal(ts provider.TerminalSession) {
	m := rt.m
	defer m.wg.Done()
	var lastError string
	for ev := range ts.Events() {
		switch ev.Kind {
		case provider.EventSession:
			_ = m.st.SetProviderSession(context.Background(), rt.id, ev.SessionID)
		case provider.EventError:
			lastError = ev.Text
		case provider.EventDone:
			rt.terminalEnded(ts, ev.Done, lastError)
			lastError = ""
		}
	}
}

// terminalEnded applies the restart policy to a program that has ended.
func (rt *runtime) terminalEnded(ts provider.TerminalSession, done *provider.Done, lastError string) {
	m := rt.m
	rt.mu.Lock()
	stopping, keep, shutdown := rt.stopping, rt.keepRunning, rt.shutdown
	ranFor := time.Since(rt.lastLaunch)
	if ranFor >= m.opts.Restart.StableAfter {
		rt.restarts = 0 // it ran long enough to count as healthy
	}
	rt.mu.Unlock()

	switch {
	case shutdown || stopping:
		if !keep {
			_ = m.st.SetRunState(context.Background(), rt.id, store.RunStopped)
		}
		m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionStopped})
		return
	case done.Reason != provider.DoneFailed:
		// The user quit the program (or interrupted it): that is not a crash.
		_ = m.st.SetRunState(context.Background(), rt.id, store.RunStopped)
		m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionStopped})
		return
	}

	// A crash.
	rt.mu.Lock()
	attempt := rt.restarts
	if attempt >= len(m.opts.Restart.Delays) {
		rt.mu.Unlock()
		_ = m.st.SetRunState(context.Background(), rt.id, store.RunStopped)
		msg := fmt.Sprintf("the program kept crashing and was restarted %d times; giving up (%s)", attempt, orDefault(lastError, "no error message"))
		_, _ = m.st.AppendAudit(context.Background(), store.AuditRow{EmployeeID: rt.id, Kind: "employee", Action: "session crashed", Detail: msg})
		m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionCrashed, Detail: msg})
		return
	}
	delay := m.opts.Restart.Delays[attempt]
	rt.restarts++
	detail := fmt.Sprintf("the program crashed; restart %d of %d in %s", attempt+1, len(m.opts.Restart.Delays), delay)
	rt.timer = time.AfterFunc(delay, func() { rt.restart(ts) })
	rt.mu.Unlock()
	m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionRestarting, Detail: detail})
}

// restart relaunches a crashed program, unless it was stopped meanwhile.
func (rt *runtime) restart(ts provider.TerminalSession) {
	rt.mu.Lock()
	rt.timer = nil
	skip := rt.stopping || rt.shutdown
	rt.mu.Unlock()
	if skip {
		return
	}
	if err := rt.launch(context.Background(), ts); err != nil {
		rt.m.hub.publish(Update{Kind: UpdateSession, EmployeeID: rt.id, State: SessionCrashed, Detail: "could not restart: " + err.Error()})
		_ = rt.m.st.SetRunState(context.Background(), rt.id, store.RunStopped)
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Recovered says what Start brought back.
type Recovered struct {
	// Interrupted is how many turns were running when the daemon last stopped
	// and are now marked as interrupted.
	Interrupted int
	// Resumed lists the employees whose terminal session was started again.
	Resumed []string
	// Failed maps employees whose session could not be brought back to why.
	Failed map[string]string
}

// Start prepares the manager after the daemon (re)starts: turns that were in
// flight are marked interrupted, and terminal sessions that were running come
// back, resuming the provider session. Call it once, before serving clients.
func (m *Manager) Start(ctx context.Context) (Recovered, error) {
	rec := Recovered{Failed: map[string]string{}}
	n, err := m.st.InterruptRunningTurns(ctx, time.Now())
	if err != nil {
		return rec, err
	}
	rec.Interrupted = n
	rows, err := m.st.EmployeesToResume(ctx)
	if err != nil {
		return rec, err
	}
	for _, row := range rows {
		if _, err := m.StartTerminal(ctx, row.ID, 0, 0); err != nil {
			rec.Failed[row.ID] = err.Error()
			_ = m.st.SetRunState(ctx, row.ID, store.RunStopped)
			continue
		}
		rec.Resumed = append(rec.Resumed, row.ID)
	}
	return rec, nil
}

// Shutdown stops everything for a clean exit of the daemon. Terminal sessions
// that were running are remembered as running, so the next Start brings them
// back; turns in flight are recorded as interrupted. It waits for the work to
// wind down until ctx ends.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	rts := make([]*runtime, 0, len(m.rts))
	for _, rt := range m.rts {
		rts = append(rts, rt)
	}
	logins := m.logins
	m.logins = map[string]provider.TerminalSession{}
	m.mu.Unlock()
	for _, l := range logins {
		_ = l.Close()
	}

	for _, rt := range rts {
		rt.mu.Lock()
		rt.shutdown, rt.stopping, rt.keepRunning = true, true, true
		cancel := rt.turnCancel
		if rt.timer != nil {
			rt.timer.Stop()
			rt.timer = nil
		}
		rt.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	for _, rt := range rts {
		rt.mu.Lock()
		sess := rt.sess
		rt.mu.Unlock()
		if sess != nil {
			_ = sess.Close()
		}
	}
	waited := make(chan struct{})
	go func() { m.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.hub.closeAll()
	return nil
}
