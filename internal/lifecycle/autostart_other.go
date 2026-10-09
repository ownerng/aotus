//go:build !windows

package lifecycle

import (
	"fmt"
	"os"
	"runtime"
)

func newPlatformAutostart(exe string) (Autostart, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("lifecycle: cannot find the home directory: %w", err)
	}
	return newFileAutostart(runtime.GOOS, home, os.Getenv("XDG_CONFIG_HOME"), exe)
}
