package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// StartDetached starts a program that must outlive the caller, such as the
// daemon started by the desktop app. It has no standard input or output, runs
// in its own session (Unix) or process group (Windows), and is not tied to a
// Job Object, so closing the caller's window or terminal does not end it. The
// child inherits the caller's environment, because the daemon needs the user's
// own (HOME, PATH to find the CLIs). It returns the PID; the child is released
// and will be reaped by the system.
func StartDetached(path string, args ...string) (int, error) {
	cmd := exec.CommandContext(context.Background(), path, args...) //nolint:gosec // the path is the daemon binary chosen by the caller; no context ends it, it must outlive us
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = devnull.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("proc: starting %s: %w", path, err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
