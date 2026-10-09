//go:build windows

package lifecycle

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKey is the per-user list of programs started at login. Tests point
// runKeyPath at a scratch key.
var runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

const runValueName = "Aotus"

type registryAutostart struct{ command string }

func newPlatformAutostart(exe string) (Autostart, error) {
	return registryAutostart{command: `"` + strings.ReplaceAll(exe, `"`, ``) + `"`}, nil
}

func (a registryAutostart) Enable() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("lifecycle: opening the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.SetStringValue(runValueName, a.command); err != nil {
		return fmt.Errorf("lifecycle: registering autostart: %w", err)
	}
	return nil
}

func (a registryAutostart) Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lifecycle: opening the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("lifecycle: removing autostart: %w", err)
	}
	return nil
}

func (a registryAutostart) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = k.Close() }()
	_, _, err = k.GetStringValue(runValueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
