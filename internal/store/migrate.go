package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// Migration is one ordered, forward-only schema change.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// migrations is the full history. Append new entries with the next version;
// never edit or reorder an applied one.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "meta",
		SQL: `CREATE TABLE app_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		) STRICT;`,
	},
	{
		Version: 2,
		Name:    "profiles_employees_audit",
		SQL: `
		CREATE TABLE profiles (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL,
			kind              TEXT NOT NULL,
			binary            TEXT NOT NULL,
			config_dir        TEXT NOT NULL UNIQUE,
			mode              TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			base_url          TEXT NOT NULL DEFAULT '',
			api_key_ref       TEXT NOT NULL DEFAULT '',
			terms_checked_at  TEXT NOT NULL DEFAULT '',
			accepted_notices  TEXT NOT NULL DEFAULT '[]',
			extra_env         TEXT NOT NULL DEFAULT '{}',
			created_at        TEXT NOT NULL
		) STRICT;

		CREATE TABLE employees (
			id               TEXT PRIMARY KEY,
			slug             TEXT NOT NULL,
			name             TEXT NOT NULL,
			role             TEXT NOT NULL DEFAULT '',
			system_prompt    TEXT NOT NULL DEFAULT '',
			profile_id       TEXT NOT NULL REFERENCES profiles(id) ON DELETE RESTRICT,
			state            TEXT NOT NULL CHECK (state IN ('active', 'paused', 'deleted')),
			permission_mode  TEXT NOT NULL DEFAULT '',
			allowed_tools    TEXT NOT NULL DEFAULT '[]',
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			deleted_at       TEXT NOT NULL DEFAULT ''
		) STRICT;
		-- A name (and its folder) can be reused after the employee was deleted.
		CREATE UNIQUE INDEX employees_slug_alive ON employees (slug) WHERE state <> 'deleted';
		CREATE INDEX employees_profile ON employees (profile_id);

		-- The audit log has no foreign key on purpose: it must outlive the
		-- employees and profiles it talks about. It is append-only, enforced by
		-- the database itself.
		CREATE TABLE audit_log (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			at           TEXT NOT NULL,
			employee_id  TEXT NOT NULL DEFAULT '',
			kind         TEXT NOT NULL,
			action       TEXT NOT NULL,
			detail       TEXT NOT NULL DEFAULT '',
			decision     TEXT NOT NULL DEFAULT ''
		) STRICT;
		CREATE INDEX audit_log_employee ON audit_log (employee_id, id);
		CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
			BEGIN SELECT RAISE(ABORT, 'the audit log is append-only'); END;
		CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
			BEGIN SELECT RAISE(ABORT, 'the audit log is append-only'); END;
		`,
	},
	{
		Version: 3,
		Name:    "permission_grants",
		SQL: `
		-- Decisions the user asked to remember. Scoped to one employee and one
		-- kind of action: a grant for one employee never applies to another.
		-- Target is the exact thing approved (a command, a path, a host) or '*'
		-- for every target of that kind.
		CREATE TABLE permission_grants (
			employee_id  TEXT NOT NULL,
			kind         TEXT NOT NULL,
			target       TEXT NOT NULL,
			decision     TEXT NOT NULL CHECK (decision IN ('allow', 'deny')),
			created_at   TEXT NOT NULL,
			PRIMARY KEY (employee_id, kind, target)
		) STRICT;
		`,
	},
}

// applyMigrations applies, in version order and each in its own transaction,
// the migrations that are not yet recorded. A failing migration is rolled back
// completely and stops the process, so the database is left at the last good
// version.
func applyMigrations(ctx context.Context, db *sql.DB, all []Migration) error {
	ms := append([]Migration(nil), all...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	for i := 1; i < len(ms); i++ {
		if ms[i].Version == ms[i-1].Version {
			return fmt.Errorf("store: duplicate migration version %d", ms[i].Version)
		}
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	) STRICT`); err != nil {
		return fmt.Errorf("store: creating schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: reading schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return fmt.Errorf("store: migration %d (%s) failed and was rolled back: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.Version, m.Name); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
