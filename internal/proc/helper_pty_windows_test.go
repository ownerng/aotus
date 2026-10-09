//go:build windows

package proc

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// runTerminalHelper holds the helper modes that run in a pseudo-console:
//
//	readline  print "ready", then "got:<line>" for every line typed
//	two       print a line, wait, print another
func runTerminalHelper(mode string, out *bufio.Writer) {
	flush := func() { _ = out.Flush() }
	switch mode {
	case "readline":
		fmt.Fprint(out, "ready\r\n")
		flush()
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Fprintf(out, "got:%s\r\n", strings.TrimSpace(sc.Text()))
			flush()
		}
	case "two":
		fmt.Fprint(out, "first-line\r\n")
		flush()
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(out, "second-line\r\n")
		flush()
	}
}
