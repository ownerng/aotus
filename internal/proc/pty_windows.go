//go:build windows

package proc

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConPTY: the Windows pseudo-console. The program runs attached to a
// pseudo-console whose two pipes we hold; it is also placed in a Job Object
// that kills its whole tree, the same guarantee proc.Start gives.
//
// This was written without a Windows machine at hand: it is checked by
// cross-compiling and by the Windows runner of the CI, and task P1-021 stays
// open until docs/research/windows-verification.md records a real run.

func ptySupported() bool { return true }

type conPTY struct {
	mu     sync.Mutex
	hpc    windows.Handle
	in     *os.File // we write the program's input here
	out    *os.File // we read the program's output here
	proc   windows.Handle
	job    windows.Handle
	pid    int
	closed bool
}

func openPTY(spec PTYSpec) (ptyBackend, error) {
	// Two pipes: the pseudo-console reads input from inR and writes output to outW.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("proc: creating the input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		_ = windows.CloseHandle(inR)
		_ = windows.CloseHandle(inW)
		return nil, fmt.Errorf("proc: creating the output pipe: %w", err)
	}
	var hpc windows.Handle
	size := windows.Coord{X: int16(spec.Cols), Y: int16(spec.Rows)} //nolint:gosec // a terminal size is small
	if err := windows.CreatePseudoConsole(size, inR, outW, 0, &hpc); err != nil {
		for _, h := range []windows.Handle{inR, inW, outR, outW} {
			_ = windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("proc: creating the pseudo-console (needs Windows 10 1809 or newer): %w", err)
	}
	// The pseudo-console keeps its own copies of its ends.
	_ = windows.CloseHandle(inR)
	_ = windows.CloseHandle(outW)

	c := &conPTY{
		hpc: hpc,
		in:  os.NewFile(uintptr(inW), "conpty-in"),
		out: os.NewFile(uintptr(outR), "conpty-out"),
	}
	if err := c.start(spec); err != nil {
		c.release()
		return nil, startError(spec.Path, err)
	}
	return c, nil
}

// start creates the program attached to the pseudo-console and puts it in a Job Object.
func (c *conPTY) start(spec PTYSpec) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpcValue(c.hpc), unsafe.Sizeof(c.hpc)); err != nil {
		return err
	}

	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))

	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{spec.Path}, spec.Args...)))
	if err != nil {
		return err
	}
	var dir *uint16
	if spec.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(spec.Dir); err != nil {
			return err
		}
	}
	env, err := environmentBlock(spec.Env)
	if err != nil {
		return err
	}

	job, err := newKillOnCloseJob()
	if err != nil {
		return err
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, &env[0], dir, &si.StartupInfo, &pi); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	// Created suspended so that it is in the job before its first instruction:
	// nothing it starts can escape.
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(pi.Process)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(job)
		return err
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(pi.Process)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(job)
		return err
	}
	_ = windows.CloseHandle(pi.Thread)
	c.proc, c.job, c.pid = pi.Process, job, int(pi.ProcessId)
	return nil
}

// hpcValue is the pseudo-console handle itself as the pointer-sized value the
// attribute list wants (not a pointer to it).
func hpcValue(h windows.Handle) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&h)) } //nolint:gosec // reinterpreting a handle as the pointer-sized attribute value is the documented use

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { //nolint:gosec // the Win32 call takes a pointer to the struct
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// environmentBlock builds the UTF-16 environment of the program from exactly
// the variables given, plus SYSTEMROOT, which Windows programs cannot run
// without.
func environmentBlock(env []string) ([]uint16, error) {
	hasRoot := false
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "SYSTEMROOT=") {
			hasRoot = true
		}
	}
	if !hasRoot {
		if root := os.Getenv("SYSTEMROOT"); root != "" {
			env = append(append([]string{}, env...), "SYSTEMROOT="+root)
		}
	}
	var block []uint16
	for _, kv := range env {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		block = append(block, u...) // each ends with its own NUL
	}
	return append(block, 0, 0), nil // an empty block still needs the double NUL
}

func (c *conPTY) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *conPTY) Write(b []byte) (int, error) { return c.in.Write(b) }
func (c *conPTY) Pid() int                    { return c.pid }

func (c *conPTY) Resize(rows, cols uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.hpc == 0 {
		return ErrPTYClosed
	}
	if err := windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)}); err != nil { //nolint:gosec // a terminal size is small
		return fmt.Errorf("proc: resizing the terminal: %w", err)
	}
	return nil
}

// Terminate ends every process in the job. Windows has no polite request for a
// whole tree, so graceful and forced are the same here.
func (c *conPTY) Terminate(bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job != 0 {
		_ = windows.TerminateJobObject(c.job, 1)
	}
}

func (c *conPTY) Wait() Exit {
	_, _ = windows.WaitForSingleObject(c.proc, windows.INFINITE)
	var code uint32
	e := Exit{Code: -1}
	if err := windows.GetExitCodeProcess(c.proc, &code); err == nil {
		e.Code = int(code)
	}
	// ConPTY delivers the program's last output only when the pseudo-console is
	// closed, and closing it can wait for the output to be read, so do it while
	// the reader keeps draining. It also ends the output pipe, which is how the
	// reader learns the program is over.
	go c.closeConsole()
	return e
}

func (c *conPTY) closeConsole() {
	c.mu.Lock()
	h := c.hpc
	c.hpc = 0
	c.mu.Unlock()
	if h != 0 {
		windows.ClosePseudoConsole(h)
	}
}

// Close closes the pseudo-console, which ends the output pipe, and releases
// every handle. Safe to call more than once.
func (c *conPTY) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.release()
	return nil
}

func (c *conPTY) release() {
	c.closeConsole()
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.out != nil {
		_ = c.out.Close()
	}
	if c.proc != 0 {
		_ = windows.CloseHandle(c.proc)
	}
	if c.job != 0 {
		_ = windows.CloseHandle(c.job) // kill-on-close: nothing outlives us
		c.job = 0
	}
}
