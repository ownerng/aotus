package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"

	"aotus/internal/datadir"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite" (ADR 0005)
)

// Store is the handle to the SQLite database.
type Store struct {
	db     *sql.DB
	layout datadir.Layout
}

// Open creates the data directory (owner-only), opens the database in WAL mode
// with foreign keys enforced, and applies any pending migrations.
func Open(ctx context.Context, layout datadir.Layout) (*Store, error) {
	if err := layout.Ensure(); err != nil {
		return nil, err
	}
	if err := restrictDatabaseFile(layout.Database()); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(layout.Database()))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", layout.Database(), err)
	}
	s := &Store{db: db, layout: layout}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connecting to %s: %w", layout.Database(), err)
	}
	if err := applyMigrations(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// restrictDatabaseFile creates the database file with owner-only permissions
// before SQLite does (SQLite would use the umask and give 0644), and tightens
// an existing one. SQLite gives its -wal and -shm files the same permissions.
// The data directory is already 0700; this is defense in depth.
func restrictDatabaseFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path is inside the data directory
	if err != nil {
		return fmt.Errorf("store: creating %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("store: restricting %s: %w", path, err)
		}
	}
	return nil
}

// dsn applies the pragmas to every connection the pool opens.
func dsn(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB is the underlying handle, for the repositories in this package and for
// tests. Other packages use the typed methods of Store, not raw SQL.
func (s *Store) DB() *sql.DB { return s.db }

// Layout is the data directory the store was opened on.
func (s *Store) Layout() datadir.Layout { return s.layout }
