package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GrantRow is a remembered permission decision.
type GrantRow struct {
	EmployeeID string
	Kind       string
	Target     string // exact target, or "*" for every target of the kind
	Allow      bool
	CreatedAt  time.Time
}

// PutGrant stores or replaces a remembered decision.
func (s *Store) PutGrant(ctx context.Context, g GrantRow) error {
	decision := "deny"
	if g.Allow {
		decision = "allow"
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO permission_grants (employee_id, kind, target, decision, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (employee_id, kind, target) DO UPDATE SET decision = excluded.decision, created_at = excluded.created_at`,
		g.EmployeeID, g.Kind, g.Target, decision, timeText(g.CreatedAt))
	return err
}

// Grant looks up a remembered decision for exactly this employee, kind and
// target. The second result is false when there is none.
func (s *Store) Grant(ctx context.Context, employeeID, kind, target string) (GrantRow, bool, error) {
	var g GrantRow
	var decision, created string
	err := s.db.QueryRowContext(ctx, `
		SELECT employee_id, kind, target, decision, created_at FROM permission_grants
		WHERE employee_id = ? AND kind = ? AND target = ?`, employeeID, kind, target).
		Scan(&g.EmployeeID, &g.Kind, &g.Target, &decision, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return GrantRow{}, false, nil
	}
	if err != nil {
		return GrantRow{}, false, err
	}
	g.Allow, g.CreatedAt = decision == "allow", parseTime(created)
	return g, true, nil
}

// Grants lists the remembered decisions of an employee.
func (s *Store) Grants(ctx context.Context, employeeID string) ([]GrantRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT employee_id, kind, target, decision, created_at FROM permission_grants
		WHERE employee_id = ? ORDER BY kind, target`, employeeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GrantRow
	for rows.Next() {
		var g GrantRow
		var decision, created string
		if err := rows.Scan(&g.EmployeeID, &g.Kind, &g.Target, &decision, &created); err != nil {
			return nil, err
		}
		g.Allow, g.CreatedAt = decision == "allow", parseTime(created)
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteGrant forgets a remembered decision (the user changed their mind).
func (s *Store) DeleteGrant(ctx context.Context, employeeID, kind, target string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM permission_grants WHERE employee_id = ? AND kind = ? AND target = ?`, employeeID, kind, target)
	return err
}
