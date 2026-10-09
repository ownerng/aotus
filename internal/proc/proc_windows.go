//go:build windows

package proc

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Windows has no process groups to signal. Instead every child is assigned to
// a Job Object configured to kill all its members when terminated or when the
// last handle to the job closes, so no descendant can outlive the daemon.
//
// Known limitation: the child is assigned to the job right after it starts,
// not before its first instruction (that would need CREATE_SUSPENDED plus the
// thread handle, which os/exec does not expose). A grandchild spawned in that
// first instant would escape. This is acceptable for the CLIs we run; revisit
// if it ever matters.

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x2000
	processSetQuota                        = 0x0100
	processTerminate                       = 0x0001
)

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// platform holds the Job Object handle.
type platform struct {
	mu  sync.Mutex // terminate may run while the supervisor closes the job
	job syscall.Handle
}

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func (p *Process) attach() error {
	job, _, err := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return err
	}
	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, err := procSetInformationJobObject.Call(job, jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)) //nolint:gosec // the Win32 call takes a pointer to the struct
	if r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return err
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(p.cmd.Process.Pid)) //nolint:gosec // PIDs fit in 32 bits
	if err != nil {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return err
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	r, _, err = procAssignProcessToJobObject.Call(job, uintptr(h))
	if r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return err
	}
	p.mu.Lock()
	p.job = syscall.Handle(job)
	p.mu.Unlock()
	return nil
}

func (p *Process) closePlatform() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job != 0 {
		_ = syscall.CloseHandle(p.job)
		p.job = 0
	}
}

// terminate ends every process in the job. Windows has no polite request for
// a whole tree, so graceful and forced are the same here.
func (p *Process) terminate(bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job != 0 {
		_, _, _ = procTerminateJobObject.Call(uintptr(p.job), 1)
	}
}

func (p *Process) interrupt() error { return ErrInterruptUnsupported }

func signalName(*os.ProcessState) string { return "" }
