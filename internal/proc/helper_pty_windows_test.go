//go:build windows

package proc

import "bufio"

// The terminal helper modes need a Unix terminal; the Windows ones arrive with
// task P1-021.
func runTerminalHelper(string, *bufio.Writer) {}
