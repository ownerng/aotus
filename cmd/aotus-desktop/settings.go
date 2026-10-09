//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Settings are the preferences of the window, kept apart from the daemon's
// data: they belong to this app, not to the employees.
type Settings struct {
	// KeepInTray keeps the app alive in the system tray after the window is
	// closed. Off by default: it costs about 170 MB (docs/BENCHMARKS.md), while
	// the daemon that runs the employees stays at about 15 MB either way.
	KeepInTray bool `json:"keep_in_tray"`
}

// SettingsStore reads and writes Settings in a JSON file.
type SettingsStore struct {
	path string
	mu   sync.Mutex
}

// NewSettingsStore uses the user's config directory when path is "".
func NewSettingsStore(path string) (*SettingsStore, error) {
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "aotus", "desktop.json")
	}
	return &SettingsStore{path: path}, nil
}

// Load returns the saved settings, or the defaults when there are none.
func (s *SettingsStore) Load() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v Settings
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &v) // a damaged file means defaults
	}
	return v
}

// Save writes the settings.
func (s *SettingsStore) Save(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
