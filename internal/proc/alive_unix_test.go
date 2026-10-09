//go:build !windows

package proc

import (
	"os"
	"strings"
	"syscall"
)

// alive reports whether a process exists and is not a zombie.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + itoa(pid) + "/stat"); err == nil {
		// Format: pid (comm) state ...; the state follows the last ')'.
		s := string(b)
		if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) && s[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
