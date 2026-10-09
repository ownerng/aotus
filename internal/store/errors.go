package store

import (
	"errors"
	"strings"
	"time"
)

// Errors returned by the repositories. Use errors.Is.
var (
	// ErrNotFound means the record does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict means a unique value is already taken.
	ErrConflict = errors.New("store: already exists")
	// ErrInUse means other records still refer to this one.
	ErrInUse = errors.New("store: still in use")
)

// classify maps SQLite constraint failures to the errors above.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return errors.Join(ErrConflict, err)
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return errors.Join(ErrInUse, err)
	}
	return err
}

// timeText stores times as UTC text, which sorts and survives any driver.
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
