package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"aotus/internal/datadir"
)

// ErrAlreadyRunning means another daemon holds the data directory.
var ErrAlreadyRunning = errors.New("another Aotus daemon is already running")

// Lock is the single-instance lock of a data directory. It is an operating
// system file lock held for the life of the process, so it is released by the
// system when the daemon exits, even if it crashes: a lock left behind by a
// crashed daemon never blocks the next start.
type Lock struct {
	f    *os.File
	path string
}

// Acquire takes the lock. If another daemon holds it, the error wraps
// ErrAlreadyRunning and says which process (read from the lock file) and where
// it listens (read from the discovery file), so the user is pointed to the
// running one.
func Acquire(l datadir.Layout) (*Lock, error) {
	if err := l.Ensure(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(l.Lock(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: opening %s: %w", l.Lock(), err)
	}
	if err := lockFile(f); err != nil {
		holder := describeHolder(l, f)
		_ = f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("%w%s", ErrAlreadyRunning, holder)
		}
		return nil, fmt.Errorf("lifecycle: locking %s: %w", l.Lock(), err)
	}
	// Record who holds it (informational; the lock itself is the truth).
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f, path: l.Lock()}, nil
}

// Release gives the lock up. The lock file is left in place on purpose:
// removing it would race with a daemon that is just starting.
func (k *Lock) Release() error {
	if k == nil || k.f == nil {
		return nil
	}
	err := unlockFile(k.f)
	if cerr := k.f.Close(); err == nil {
		err = cerr
	}
	k.f = nil
	return err
}

// describeHolder returns " (process N, listening on A)" from what the running
// daemon wrote, or "" if that cannot be read.
func describeHolder(l datadir.Layout, f *os.File) string {
	var parts []string
	buf := make([]byte, 32)
	if n, _ := f.ReadAt(buf, 0); n > 0 {
		if pid := strings.TrimSpace(string(buf[:n])); pid != "" {
			parts = append(parts, "process "+pid)
		}
	}
	if d, err := ReadDiscovery(l); err == nil && d.Address != "" {
		parts = append(parts, "listening on "+d.Address)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
