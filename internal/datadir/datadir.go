// Package datadir defines where Aotus keeps its files and creates the folders
// with owner-only permissions. Every package that needs a path under the data
// directory asks this one, so the layout lives in a single place.
//
// It is a leaf.
package datadir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnvHome overrides the default location (used by tests and portable setups).
const EnvHome = "AOTUS_HOME"

// Layout is the set of paths under one data directory.
type Layout struct {
	Root string
}

// Default is $AOTUS_HOME when set, otherwise ~/.aotus.
func Default() (Layout, error) {
	if v := os.Getenv(EnvHome); v != "" {
		return Layout{Root: v}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("datadir: cannot find the home directory: %w", err)
	}
	return Layout{Root: filepath.Join(home, ".aotus")}, nil
}

// Database is the SQLite file.
func (l Layout) Database() string { return filepath.Join(l.Root, "aotus.db") }

// Discovery is the file clients read to find the running daemon.
func (l Layout) Discovery() string { return filepath.Join(l.Root, "daemon.json") }

// Lock is the single-instance lock file.
func (l Layout) Lock() string { return filepath.Join(l.Root, "daemon.lock") }

// Token is the local API token.
func (l Layout) Token() string { return filepath.Join(l.Root, "token") }

// ProfilesDir holds one isolated configuration directory per profile.
func (l Layout) ProfilesDir() string { return filepath.Join(l.Root, "profiles") }

// ProfileDir is the configuration directory of one profile.
func (l Layout) ProfileDir(id string) string { return filepath.Join(l.ProfilesDir(), id) }

// EmployeesDir holds one working folder per employee.
func (l Layout) EmployeesDir() string { return filepath.Join(l.Root, "employees") }

// EmployeeDir is the working folder of one employee.
func (l Layout) EmployeeDir(name string) string { return filepath.Join(l.EmployeesDir(), name) }

// TrashDir holds folders of deleted employees.
func (l Layout) TrashDir() string { return filepath.Join(l.Root, "trash") }

// Ensure creates the root and its fixed subfolders with owner-only
// permissions (0700). The root's permissions are tightened even if it already
// existed, because it holds the API token and every employee's files.
func (l Layout) Ensure() error {
	if l.Root == "" {
		return fmt.Errorf("datadir: empty root")
	}
	for _, dir := range []string{l.Root, l.ProfilesDir(), l.EmployeesDir(), l.TrashDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("datadir: creating %s: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directories need the execute bit; 0700 is owner-only
				return fmt.Errorf("datadir: restricting %s: %w", dir, err)
			}
		}
	}
	return nil
}
