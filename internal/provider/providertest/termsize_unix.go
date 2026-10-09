//go:build !windows

package providertest

import (
	"fmt"
	"os"

	"github.com/creack/pty"
)

// termSize is the size of the terminal on stdin, as "<rows>x<cols>".
func termSize() string {
	rows, cols, err := pty.Getsize(os.Stdin)
	if err != nil {
		return "error"
	}
	return fmt.Sprintf("%dx%d", rows, cols)
}
