package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrAcknowledgementRequired means a login that is not the owner's cannot be
// added without the stored acknowledgement about sharing a subscription.
var ErrAcknowledgementRequired = errors.New("store: adding another person needs the acknowledgement about sharing a subscription")

// ErrIsOwner means the login is the owner's, who is always allowed.
var ErrIsOwner = errors.New("store: that login is the owner")

const metaOwner = "owner_login"

// normLogin makes logins comparable: tailnet login names are e-mail-like and
// not case sensitive.
func normLogin(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// OwnerLogin returns the tailnet login of the person who owns this daemon, or
// "" while none was recorded (then no remote caller is served).
func (s *Store) OwnerLogin(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = ?`, metaOwner).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetOwnerLogin records the owner. It is written once, when the daemon is set
// up for remote use; changing it is a deliberate act of the owner on this
// computer.
func (s *Store) SetOwnerLogin(ctx context.Context, login string) error {
	login = normLogin(login)
	if login == "" {
		return errors.New("store: the owner login is empty")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, metaOwner, login)
	return err
}

// AccessEntry is a person allowed besides the owner.
type AccessEntry struct {
	Login   string
	AddedAt time.Time
	AddedBy string
	Notice  string // the exact text that was acknowledged
}

// AddAccess allows another person. notice is the text of the acknowledgement
// the owner accepted; it must not be empty.
func (s *Store) AddAccess(ctx context.Context, login, addedBy, notice string, at time.Time) error {
	login = normLogin(login)
	if login == "" {
		return errors.New("store: the login is empty")
	}
	if strings.TrimSpace(notice) == "" {
		return ErrAcknowledgementRequired
	}
	owner, err := s.OwnerLogin(ctx)
	if err != nil {
		return err
	}
	if login == owner {
		return ErrIsOwner
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO access_list (login, added_at, added_by, notice, acknowledged) VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(login) DO UPDATE SET added_at = excluded.added_at, added_by = excluded.added_by, notice = excluded.notice`,
		login, timeText(at), addedBy, notice)
	return err
}

// RemoveAccess takes a person off the list and forgets which profiles were
// shared with them.
func (s *Store) RemoveAccess(ctx context.Context, login string) error {
	login = normLogin(login)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_shares WHERE login = ?`, login); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM access_list WHERE login = ?`, login); err != nil {
		return err
	}
	return tx.Commit()
}

// AccessList returns the people allowed besides the owner.
func (s *Store) AccessList(ctx context.Context) ([]AccessEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT login, added_at, added_by, notice FROM access_list ORDER BY login`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AccessEntry
	for rows.Next() {
		var e AccessEntry
		var at string
		if err := rows.Scan(&e.Login, &at, &e.AddedBy, &e.Notice); err != nil {
			return nil, err
		}
		e.AddedAt = parseTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Role is what a remote login may do.
type Role int

const (
	// RoleNone is refused.
	RoleNone Role = iota
	// RoleGuest is on the list: sees everything, uses only profiles the owner
	// shared with them.
	RoleGuest
	// RoleOwner is the owner of this daemon.
	RoleOwner
)

// RoleOf says what a login is.
func (s *Store) RoleOf(ctx context.Context, login string) (Role, error) {
	login = normLogin(login)
	if login == "" {
		return RoleNone, nil
	}
	owner, err := s.OwnerLogin(ctx)
	if err != nil {
		return RoleNone, err
	}
	if owner != "" && login == owner {
		return RoleOwner, nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_list WHERE login = ?`, login).Scan(&n); err != nil {
		return RoleNone, err
	}
	if n > 0 && owner != "" {
		return RoleGuest, nil
	}
	return RoleNone, nil
}

// ShareProfile records that the owner lets login use the profile.
func (s *Store) ShareProfile(ctx context.Context, profileID, login, by string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO profile_shares (profile_id, login, confirmed_at, confirmed_by) VALUES (?, ?, ?, ?)`,
		profileID, normLogin(login), timeText(at), by)
	return err
}

// UnshareProfile withdraws it.
func (s *Store) UnshareProfile(ctx context.Context, profileID, login string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM profile_shares WHERE profile_id = ? AND login = ?`, profileID, normLogin(login))
	return err
}

// ProfileShared reports whether login may use the profile.
func (s *Store) ProfileShared(ctx context.Context, profileID, login string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profile_shares WHERE profile_id = ? AND login = ?`, profileID, normLogin(login)).Scan(&n)
	return n > 0, err
}

// ProfileShares lists who may use a profile.
func (s *Store) ProfileShares(ctx context.Context, profileID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT login FROM profile_shares WHERE profile_id = ? ORDER BY login`, profileID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
