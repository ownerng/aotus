//go:build !windows

package providertest

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Alive reports whether a process exists and is not a zombie.
func Alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		s := string(b) // format: pid (comm) state ...
		if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) && s[i+2] == 'Z' {
			return false
		}
	}
	return true
}
