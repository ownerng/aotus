//go:build windows

package providertest

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// Alive reports whether a process with this ID is still running.
func Alive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
