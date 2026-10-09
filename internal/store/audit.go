package store

import (
	"context"
	"time"
)

// AuditRow is one entry of the audit log.
type AuditRow struct {
	ID         int64
	At         time.Time
	EmployeeID string // may refer to an employee that no longer exists
	Kind       string // for example "employee", "permission"
	Action     string
	Detail     string
	Decision   string
}

// AppendAudit adds an entry. There is deliberately no function that changes or
// removes one, and the database refuses UPDATE and DELETE on the table.
func (s *Store) AppendAudit(ctx context.Context, a AuditRow) (int64, error) {
	if a.At.IsZero() {
		a.At = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (at, employee_id, kind, action, detail, decision) VALUES (?, ?, ?, ?, ?, ?)`,
		timeText(a.At), a.EmployeeID, a.Kind, a.Action, a.Detail, a.Decision)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Audit lists entries, oldest first; an empty employeeID means all employees.
// A limit of 0 means no limit.
func (s *Store) Audit(ctx context.Context, employeeID string, limit int) ([]AuditRow, error) {
	q := `SELECT id, at, employee_id, kind, action, detail, decision FROM audit_log`
	var args []any
	if employeeID != "" {
		q += ` WHERE employee_id = ?`
		args = append(args, employeeID)
	}
	q += ` ORDER BY id`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AuditRow
	for rows.Next() {
		var a AuditRow
		var at string
		if err := rows.Scan(&a.ID, &at, &a.EmployeeID, &a.Kind, &a.Action, &a.Detail, &a.Decision); err != nil {
			return nil, err
		}
		a.At = parseTime(at)
		out = append(out, a)
	}
	return out, rows.Err()
}
