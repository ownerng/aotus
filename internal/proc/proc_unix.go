//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// platform holds per-OS process state: on Unix, the process group to signal.
type platform struct {
	pgid int
}

// prepare puts the child in its own process group, so that signalling the
// negative PID reaches the child and everything it spawns.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// attach records the process group. The child was started with Setpgid, so its
// group ID is its own PID.
func (p *Process) attach() error {
	p.pgid = p.cmd.Process.Pid
	return nil
}

func (p *Process) closePlatform() {}

// terminate signals the whole process group. force kills; otherwise it asks
// politely with SIGTERM.
func (p *Process) terminate(force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-p.pgid, sig)
}

func (p *Process) interrupt() error {
	return syscall.Kill(-p.pgid, syscall.SIGINT)
}

func signalName(ps *os.ProcessState) string {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
