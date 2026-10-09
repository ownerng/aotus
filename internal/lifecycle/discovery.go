package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aotus/internal/datadir"
)

// Discovery is what a running daemon publishes so that clients can find it.
// It carries no secret: the token is a separate owner-only file.
type Discovery struct {
	PID       int       `json:"pid"`
	Address   string    `json:"address"` // host:port, always loopback in phase 1
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
}

// ErrNoDaemon means no discovery file exists.
var ErrNoDaemon = errors.New("lifecycle: no daemon has published its address")

// WriteDiscovery publishes the daemon's address atomically with owner-only
// permissions, so a client never reads half a file.
func WriteDiscovery(l datadir.Layout, d Discovery) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(l.Root, "daemon-*.json.tmp") // CreateTemp makes the file 0600
	if err != nil {
		return fmt.Errorf("lifecycle: writing the discovery file: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("lifecycle: writing the discovery file: %w", werr)
	}
	if err := os.Rename(name, l.Discovery()); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("lifecycle: publishing the discovery file: %w", err)
	}
	return nil
}

// ReadDiscovery reads the published address. The file can be stale after a
// crash; a client confirms by connecting.
func ReadDiscovery(l datadir.Layout) (Discovery, error) {
	b, err := os.ReadFile(l.Discovery())
	if errors.Is(err, os.ErrNotExist) {
		return Discovery{}, ErrNoDaemon
	}
	if err != nil {
		return Discovery{}, err
	}
	var d Discovery
	if err := json.Unmarshal(b, &d); err != nil {
		return Discovery{}, fmt.Errorf("lifecycle: unreadable discovery file %s: %w", filepath.Base(l.Discovery()), err)
	}
	return d, nil
}

// RemoveDiscovery withdraws the address on a clean shutdown.
func RemoveDiscovery(l datadir.Layout) error {
	if err := os.Remove(l.Discovery()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
