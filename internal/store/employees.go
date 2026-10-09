package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Employee states.
const (
	StateActive  = "active"
	StatePaused  = "paused"
	StateDeleted = "deleted"
)

// EmployeeRow is a stored employee.
type EmployeeRow struct {
	ID             string
	Slug           string // folder name; unique among employees that are not deleted
	Name           string
	Role           string
	SystemPrompt   string
	ProfileID      string
	State          string
	PermissionMode string
	AllowedTools   []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      time.Time
}

const employeeColumns = `id, slug, name, role, system_prompt, profile_id, state, permission_mode, allowed_tools, created_at, updated_at, deleted_at`

// CreateEmployee inserts an employee. ErrConflict if another live employee has
// the same slug; ErrInUse if the profile does not exist.
func (s *Store) CreateEmployee(ctx context.Context, e EmployeeRow) error {
	tools, _ := json.Marshal(nonNilStrings(e.AllowedTools))
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO employees (`+employeeColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Slug, e.Name, e.Role, e.SystemPrompt, e.ProfileID, e.State, e.PermissionMode, string(tools),
		timeText(e.CreatedAt), timeText(e.UpdatedAt), timeText(e.DeletedAt))
	return classify(err)
}

func scanEmployee(r scanner) (EmployeeRow, error) {
	var e EmployeeRow
	var tools, created, updated, deleted string
	if err := r.Scan(&e.ID, &e.Slug, &e.Name, &e.Role, &e.SystemPrompt, &e.ProfileID, &e.State, &e.PermissionMode,
		&tools, &created, &updated, &deleted); err != nil {
		return EmployeeRow{}, err
	}
	if err := json.Unmarshal([]byte(tools), &e.AllowedTools); err != nil {
		return EmployeeRow{}, fmt.Errorf("store: employee %s has unreadable tools: %w", e.ID, err)
	}
	e.CreatedAt, e.UpdatedAt, e.DeletedAt = parseTime(created), parseTime(updated), parseTime(deleted)
	return e, nil
}

// Employee returns one employee, deleted ones included.
func (s *Store) Employee(ctx context.Context, id string) (EmployeeRow, error) {
	e, err := scanEmployee(s.db.QueryRowContext(ctx, `SELECT `+employeeColumns+` FROM employees WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return EmployeeRow{}, ErrNotFound
	}
	return e, err
}

// Employees lists employees, oldest first. Deleted ones are only included on
// request.
func (s *Store) Employees(ctx context.Context, includeDeleted bool) ([]EmployeeRow, error) {
	q := `SELECT ` + employeeColumns + ` FROM employees`
	if !includeDeleted {
		q += ` WHERE state <> 'deleted'`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at, id`)
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

// SetEmployeeState changes the state of an employee that is not deleted. A
// deleted employee never comes back: ErrNotFound.
func (s *Store) SetEmployeeState(ctx context.Context, id, state string, at time.Time) error {
	var deleted string
	if state == StateDeleted {
		deleted = timeText(at)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE employees SET state = ?, updated_at = ?, deleted_at = ? WHERE id = ? AND state <> 'deleted'`,
		state, timeText(at), deleted, id)
	if err != nil {
		return classify(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
