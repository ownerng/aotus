package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ProfileRow is a stored subscription profile. It holds no secrets: an API key
// is only referenced by APIKeyRef, which names an entry in the operating
// system credential store.
type ProfileRow struct {
	ID, Name, Kind, Binary, ConfigDir string
	Mode, Model, BaseURL, APIKeyRef   string
	TermsCheckedAt                    string
	AcceptedNotices                   []string
	ExtraEnv                          map[string]string
	CreatedAt                         time.Time
}

// PutProfile creates or updates a profile.
func (s *Store) PutProfile(ctx context.Context, p ProfileRow) error {
	notices, _ := json.Marshal(nonNilStrings(p.AcceptedNotices))
	env, _ := json.Marshal(nonNilMap(p.ExtraEnv))
	created := p.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO profiles (id, name, kind, binary, config_dir, mode, model, base_url, api_key_ref, terms_checked_at, accepted_notices, extra_env, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, kind = excluded.kind, binary = excluded.binary, config_dir = excluded.config_dir,
			mode = excluded.mode, model = excluded.model, base_url = excluded.base_url, api_key_ref = excluded.api_key_ref,
			terms_checked_at = excluded.terms_checked_at, accepted_notices = excluded.accepted_notices, extra_env = excluded.extra_env`,
		p.ID, p.Name, p.Kind, p.Binary, p.ConfigDir, p.Mode, p.Model, p.BaseURL, p.APIKeyRef, p.TermsCheckedAt,
		string(notices), string(env), timeText(created))
	return classify(err)
}

const profileColumns = `id, name, kind, binary, config_dir, mode, model, base_url, api_key_ref, terms_checked_at, accepted_notices, extra_env, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanProfile(r scanner) (ProfileRow, error) {
	var p ProfileRow
	var notices, env, created string
	if err := r.Scan(&p.ID, &p.Name, &p.Kind, &p.Binary, &p.ConfigDir, &p.Mode, &p.Model, &p.BaseURL, &p.APIKeyRef,
		&p.TermsCheckedAt, &notices, &env, &created); err != nil {
		return ProfileRow{}, err
	}
	if err := json.Unmarshal([]byte(notices), &p.AcceptedNotices); err != nil {
		return ProfileRow{}, fmt.Errorf("store: profile %s has unreadable notices: %w", p.ID, err)
	}
	if err := json.Unmarshal([]byte(env), &p.ExtraEnv); err != nil {
		return ProfileRow{}, fmt.Errorf("store: profile %s has unreadable environment: %w", p.ID, err)
	}
	p.CreatedAt = parseTime(created)
	return p, nil
}

// RemovedProfileID is the placeholder that employees deleted earlier point at
// once their profile has been removed.
const RemovedProfileID = "removed"

// Profile returns one profile.
func (s *Store) Profile(ctx context.Context, id string) (ProfileRow, error) {
	if id == RemovedProfileID {
		return ProfileRow{}, ErrNotFound
	}
	p, err := scanProfile(s.db.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileRow{}, ErrNotFound
	}
	return p, err
}

// Profiles lists all profiles, oldest first.
func (s *Store) Profiles(ctx context.Context) ([]ProfileRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileColumns+` FROM profiles WHERE id <> 'removed' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProfileRow
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProfile removes a profile; ErrInUse if an employee that is not deleted
// still uses it. Employees deleted earlier are pointed at the placeholder so
// that their history stays.
func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	if id == RemovedProfileID {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE employees SET profile_id = 'removed' WHERE profile_id = ? AND state = 'deleted'`, id); err != nil {
		return classify(err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return classify(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilMap(v map[string]string) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return v
}
