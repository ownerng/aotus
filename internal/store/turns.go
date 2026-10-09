package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Turn states.
const (
	TurnQueued      = "queued"
	TurnRunning     = "running"
	TurnCompleted   = "completed"
	TurnCanceled    = "canceled"
	TurnFailed      = "failed"
	TurnInterrupted = "interrupted" // the daemon stopped while the turn ran
)

// TurnRow is one request to an employee and how it ended.
type TurnRow struct {
	ID, EmployeeID, Prompt, State, Error string
	StartedAt, EndedAt                   time.Time
	CostUSD                              float64
	InputTokens, OutputTokens            int
}

// TurnEventRow is one stored event of a turn.
type TurnEventRow struct {
	Seq     int64
	TurnID  string
	Kind    string
	Payload string // JSON
}

// BeginTurn records a new turn.
func (s *Store) BeginTurn(ctx context.Context, t TurnRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO turns (id, employee_id, prompt, state, started_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.EmployeeID, t.Prompt, t.State, timeText(t.StartedAt))
	return classify(err)
}

// SetTurnState changes the state of a turn that has not ended.
func (s *Store) SetTurnState(ctx context.Context, id, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET state = ? WHERE id = ? AND ended_at = ''`, state, id)
	return err
}

// EndTurn closes a turn.
func (s *Store) EndTurn(ctx context.Context, t TurnRow) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE turns SET state = ?, error = ?, ended_at = ?, cost_usd = ?, input_tokens = ?, output_tokens = ? WHERE id = ?`,
		t.State, t.Error, timeText(t.EndedAt), t.CostUSD, t.InputTokens, t.OutputTokens, t.ID)
	return err
}

// AddTurnEvent appends an event to a turn and returns its sequence number.
func (s *Store) AddTurnEvent(ctx context.Context, turnID, kind, payload string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO turn_events (turn_id, kind, payload) VALUES (?, ?, ?)`, turnID, kind, payload)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const turnColumns = `id, employee_id, prompt, state, error, started_at, ended_at, cost_usd, input_tokens, output_tokens`

func scanTurn(r scanner) (TurnRow, error) {
	var t TurnRow
	var started, ended string
	if err := r.Scan(&t.ID, &t.EmployeeID, &t.Prompt, &t.State, &t.Error, &started, &ended, &t.CostUSD, &t.InputTokens, &t.OutputTokens); err != nil {
		return TurnRow{}, err
	}
	t.StartedAt, t.EndedAt = parseTime(started), parseTime(ended)
	return t, nil
}

// Turn returns one turn.
func (s *Store) Turn(ctx context.Context, id string) (TurnRow, error) {
	t, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TurnRow{}, ErrNotFound
	}
	return t, err
}

// Turns lists an employee's turns, newest first. before, when set, returns
// only turns that started earlier (to page back through a long history); the
// UI loads history on demand with it.
func (s *Store) Turns(ctx context.Context, employeeID string, limit int, before time.Time) ([]TurnRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + turnColumns + ` FROM turns WHERE employee_id = ?`
	args := []any{employeeID}
	if !before.IsZero() {
		q += ` AND started_at < ?`
		args = append(args, timeText(before))
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY started_at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TurnRow
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TurnEvents returns the events of a turn in order.
func (s *Store) TurnEvents(ctx context.Context, turnID string) ([]TurnEventRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, turn_id, kind, payload FROM turn_events WHERE turn_id = ? ORDER BY seq`, turnID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TurnEventRow
	for rows.Next() {
		var e TurnEventRow
		if err := rows.Scan(&e.Seq, &e.TurnID, &e.Kind, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InterruptRunningTurns marks as interrupted every turn that was queued or
// running when the daemon last stopped, and returns how many there were. It
// runs at startup.
func (s *Store) InterruptRunningTurns(ctx context.Context, at time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE turns SET state = 'interrupted', error = 'the daemon stopped while this turn was running', ended_at = ?
		WHERE state IN ('queued', 'running')`, timeText(at))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// EmployeesToResume lists employees that had a terminal session running and
// are still active.
func (s *Store) EmployeesToResume(ctx context.Context) ([]EmployeeRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+employeeColumns+` FROM employees WHERE run_state = 'running' AND state = 'active' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EmployeeRow
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
