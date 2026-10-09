//go:build !windows

package proc

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

func ptySupported() bool { return true }

type unixPTY struct {
	ptmx *os.File
	cmd  *exec.Cmd
	pgid int
}

// openPTY starts the program on a new pseudo-terminal. creack/pty makes the
// child a session leader with the terminal as its controlling terminal, so its
// process group ID is its PID and signalling the negative PID reaches the
// whole tree.
func openPTY(spec PTYSpec) (ptyBackend, error) {
	cmd := exec.Command(spec.Path, spec.Args...) //nolint:gosec,noctx // running a caller-chosen binary is this package's purpose; Cancel manages the lifetime
	if cmd.Err != nil {
		return nil, startError(spec.Path, cmd.Err)
	}
	cmd.Dir = spec.Dir
	cmd.Env = append([]string{}, spec.Env...) // non-nil: never inherit
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: spec.Rows, Cols: spec.Cols})
	if err != nil {
		return nil, startError(spec.Path, err)
	}
	return &unixPTY{ptmx: ptmx, cmd: cmd, pgid: cmd.Process.Pid}, nil
}

func (u *unixPTY) Read(b []byte) (int, error)  { return u.ptmx.Read(b) }
func (u *unixPTY) Write(b []byte) (int, error) { return u.ptmx.Write(b) }
func (u *unixPTY) Close() error                { return u.ptmx.Close() }
func (u *unixPTY) Pid() int                    { return u.cmd.Process.Pid }

func (u *unixPTY) Resize(rows, cols uint16) error {
	if err := pty.Setsize(u.ptmx, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
		return fmt.Errorf("proc: resizing the terminal: %w", err)
	}
	return nil
}

func (u *unixPTY) Terminate(force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-u.pgid, sig)
}

func (u *unixPTY) Wait() Exit {
	_ = u.cmd.Wait()
	e := Exit{Code: -1}
	if ps := u.cmd.ProcessState; ps != nil {
		e.Code = ps.ExitCode()
		e.Signal = signalName(ps)
	}
	return e
}
