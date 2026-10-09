package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Limits that keep one runaway employee from filling the disk or the index.
const (
	maxFactBytes = 16 * 1024
	maxNoteBytes = 1 << 20
	maxNotes     = 5000
)

// ErrTooLarge means a fact or note exceeds the limits above.
var ErrTooLarge = errors.New("store: memory entry is too large")

// Fact is something an employee remembers.
type Fact struct {
	ID         int64
	EmployeeID string
	Key        string
	Body       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AddFact remembers a fact for one employee and indexes it for search.
func (s *Store) AddFact(ctx context.Context, employeeID, key, body string, at time.Time) (int64, error) {
	if employeeID == "" || strings.TrimSpace(body) == "" {
		return 0, errors.New("store: a fact needs an employee and some text")
	}
	if len(body) > maxFactBytes || len(key) > 256 {
		return 0, ErrTooLarge
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO memory_facts (employee_id, key, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		employeeID, key, body, timeText(at), timeText(at))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_fts (body, employee_id, kind, ref) VALUES (?, ?, 'fact', ?)`,
		key+"\n"+body, employeeID, strconv.FormatInt(id, 10)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Facts lists the facts of one employee, oldest first.
func (s *Store) Facts(ctx context.Context, employeeID string) ([]Fact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, employee_id, key, body, created_at, updated_at FROM memory_facts WHERE employee_id = ? ORDER BY id`, employeeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Fact
	for rows.Next() {
		var f Fact
		var created, updated string
		if err := rows.Scan(&f.ID, &f.EmployeeID, &f.Key, &f.Body, &created, &updated); err != nil {
			return nil, err
		}
		f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFact forgets a fact. The employee ID is part of the match, so one
// employee can never delete another's.
func (s *Store) DeleteFact(ctx context.Context, employeeID string, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM memory_facts WHERE id = ? AND employee_id = ?`, id, employeeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE kind = 'fact' AND ref = ? AND employee_id = ?`, strconv.FormatInt(id, 10), employeeID); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncResult says what SyncNotes did.
type SyncResult struct {
	Indexed int // new or changed notes
	Removed int // notes that no longer exist
	Skipped int // files left out: too large, or too many notes
}

// SyncNotes brings the search index in line with the Markdown files under dir
// (the employee's memory folder): new and edited files are indexed, deleted
// ones are dropped. The files are the source of truth, so a note the user
// edited by hand is picked up the next time this runs. Symbolic links are
// never followed, so a link cannot pull files from elsewhere into memory.
func (s *Store) SyncNotes(ctx context.Context, employeeID, dir string) (SyncResult, error) {
	var res SyncResult
	if employeeID == "" {
		return res, errors.New("store: SyncNotes needs an employee")
	}
	type seen struct {
		mtime int64
		size  int64
	}
	found := map[string]seen{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return filepath.SkipAll // no memory folder yet: nothing to index
			}
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if info.Size() > maxNoteBytes || len(found) >= maxNotes {
			res.Skipped++
			return nil
		}
		found[filepath.ToSlash(rel)] = seen{info.ModTime().UnixNano(), info.Size()}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("store: scanning %s: %w", dir, err)
	}

	known := map[string]seen{}
	rows, err := s.db.QueryContext(ctx, `SELECT path, mtime_ns, size FROM memory_notes WHERE employee_id = ?`, employeeID)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var p string
		var sn seen
		if err := rows.Scan(&p, &sn.mtime, &sn.size); err != nil {
			_ = rows.Close()
			return res, err
		}
		known[p] = sn
	}
	if err := rows.Close(); err != nil {
		return res, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	for rel, sn := range found {
		if known[rel] == sn {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // the path comes from walking the employee's memory folder
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed between the scan and now
			}
			return res, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE kind = 'note' AND ref = ? AND employee_id = ?`, rel, employeeID); err != nil {
			return res, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_fts (body, employee_id, kind, ref) VALUES (?, ?, 'note', ?)`, rel+"\n"+string(body), employeeID, rel); err != nil {
			return res, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_notes (employee_id, path, mtime_ns, size) VALUES (?, ?, ?, ?)
			ON CONFLICT (employee_id, path) DO UPDATE SET mtime_ns = excluded.mtime_ns, size = excluded.size`, employeeID, rel, sn.mtime, sn.size); err != nil {
			return res, err
		}
		res.Indexed++
	}
	for rel := range known {
		if _, still := found[rel]; still {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_fts WHERE kind = 'note' AND ref = ? AND employee_id = ?`, rel, employeeID); err != nil {
			return res, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_notes WHERE employee_id = ? AND path = ?`, employeeID, rel); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, tx.Commit()
}

// Hit is one search result.
type Hit struct {
	Kind    string // "fact" or "note"
	Ref     string // the fact ID, or the note path relative to the memory folder
	Snippet string
}

// SearchMemory finds facts and notes of one employee that match the words in
// query (all of them, in any order; the last one may be a prefix). The
// employee is mandatory and always part of the match: there is no way to
// search across employees.
func (s *Store) SearchMemory(ctx context.Context, employeeID, query string, limit int) ([]Hit, error) {
	if employeeID == "" {
		return nil, errors.New("store: a memory search needs an employee")
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, ref, snippet(memory_fts, 0, '[', ']', '...', 12)
		FROM memory_fts
		WHERE memory_fts MATCH ? AND employee_id = ?
		ORDER BY bm25(memory_fts)
		LIMIT ?`, match, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Kind, &h.Ref, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery turns free text into a safe FTS5 query: every word becomes a quoted
// term (so punctuation and operators in the text mean nothing), all terms must
// match, and the last one also matches as a prefix.
func ftsQuery(text string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r <= 127
	}) {
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " ")
}

// SetEmployeeProfile binds an employee to another profile. Memory is keyed by
// employee, not by profile, so it stays with the employee (groundwork for
// changing the profile of an employee in a later phase).
func (s *Store) SetEmployeeProfile(ctx context.Context, employeeID, profileID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE employees SET profile_id = ?, updated_at = ? WHERE id = ? AND state <> 'deleted'`, profileID, timeText(at), employeeID)
	if err != nil {
		return classify(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
