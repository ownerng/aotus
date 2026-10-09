package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aotus/internal/datadir"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), datadir.Layout{Root: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesDataDir(t *testing.T) {
	s := openTemp(t)
	l := s.Layout()
	for _, p := range []string{l.Root, l.Database(), l.ProfilesDir(), l.EmployeesDir(), l.TrashDir()} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must exist after Open: %v", p, err)
		}
	}
}

func TestDataDirIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not apply on Windows")
	}
	s := openTemp(t)
	for _, p := range []string{s.Layout().Root, s.Layout().ProfilesDir(), s.Layout().EmployeesDir(), s.Layout().TrashDir()} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o, want 700", p, fi.Mode().Perm())
		}
	}
}

func TestDatabaseFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not apply on Windows")
	}
	s := openTemp(t)
	if _, err := s.DB().ExecContext(context.Background(), `INSERT INTO app_meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err) // makes SQLite create the -wal and -shm files
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(s.Layout().Database() + suffix)
		if err != nil {
			continue // SQLite may not have created it yet
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("aotus.db%s mode = %o, want 600", suffix, fi.Mode().Perm())
		}
	}
}

func TestOpenUsesWAL(t *testing.T) {
	s := openTemp(t)
	var mode string
	if err := s.DB().QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.DB().QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v; want 1", fk, err)
	}
}

func rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "raw.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func appliedVersions(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT version FROM schema_migrations ORDER BY applied_at, version`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, string(rune('0'+v)))
	}
	return strings.Join(out, ",")
}

func TestMigrationsApplyInOrderOnce(t *testing.T) {
	db := rawDB(t)
	list := []Migration{ // deliberately out of order
		{Version: 2, Name: "second", SQL: `INSERT INTO t (v) VALUES ('two')`},
		{Version: 1, Name: "first", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`},
	}
	ctx := context.Background()
	if err := applyMigrations(ctx, db, list); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, db, list); err != nil { // second run must change nothing
		t.Fatal(err)
	}
	if got := appliedVersions(t, db); got != "1,2" {
		t.Fatalf("applied versions = %s, want 1,2 (in order, once)", got)
	}
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v; migration 2 must have run exactly once", n, err)
	}
	if err := applyMigrations(ctx, db, []Migration{{Version: 1, SQL: "SELECT 1"}, {Version: 1, SQL: "SELECT 1"}}); err == nil {
		t.Fatal("duplicate versions must be rejected")
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	db := rawDB(t)
	ctx := context.Background()
	good := Migration{Version: 1, Name: "good", SQL: `CREATE TABLE a (id INTEGER)`}
	bad := Migration{Version: 2, Name: "bad", SQL: `CREATE TABLE b (id INTEGER); INSERT INTO missing_table VALUES (1);`}

	err := applyMigrations(ctx, db, []Migration{good, bad})
	if err == nil || !strings.Contains(err.Error(), "migration 2") {
		t.Fatalf("err = %v, want a failure naming migration 2", err)
	}
	if got := appliedVersions(t, db); got != "1" {
		t.Fatalf("applied = %s, want only 1", got)
	}
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("table b exists (%d, %v): the failed migration must be rolled back completely", n, err)
	}
}

func TestOpenIsRepeatable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	for i := 0; i < 2; i++ {
		s, err := Open(context.Background(), datadir.Layout{Root: root})
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		_ = s.Close()
	}
}
