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
