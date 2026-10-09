//go:build windows

package providertest

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// termSize is the size of the console window ("rows x columns"), which in a
// ConPTY is the size of the pseudo-console.
func termSize() string {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Stdout, &info); err != nil {
		return "n/a"
	}
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	cols := int(info.Window.Right-info.Window.Left) + 1
	return fmt.Sprintf("%dx%d", rows, cols)
}
