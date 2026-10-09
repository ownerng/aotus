//go:build windows

package lifecycle

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errLocked = errors.New("locked by another process")

// lockOffset is where the one-byte lock sits: far beyond the PID text at the
// start of the file. Windows locks are mandatory, so a lock on the first byte
// would stop everyone else (the second daemon, a test) from reading the PID.
const lockOffset = 1 << 30

func lockRegion() *windows.Overlapped { return &windows.Overlapped{Offset: lockOffset} }

// lockFile takes an exclusive, non-blocking lock on one byte of the file
// (LockFileEx). Windows releases it when the process ends.
func lockFile(f *os.File) error {
	ol := lockRegion()
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := lockRegion()
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
