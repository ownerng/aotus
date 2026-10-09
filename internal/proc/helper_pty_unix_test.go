//go:build !windows

package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// runTerminalHelper holds the helper modes that need a terminal:
//
//	tty       print whether stdin is a terminal and its size
//	winch     print the size now and again on every SIGWINCH
//	readline  print "got:<line>" for every line typed
//	two       print a line, wait, print another
//	ticker    print "tick N" every 100 ms
//	flood     print about 20 MiB and then "flood-end"
func runTerminalHelper(mode string, out *bufio.Writer) {
	flush := func() { _ = out.Flush() }
	size := func() string {
		rows, cols, err := pty.Getsize(os.Stdin)
		if err != nil {
			return "size:error"
		}
		return fmt.Sprintf("size:%dx%d", rows, cols)
	}
	switch mode {
	case "tty":
		fi, err := os.Stdin.Stat()
		fmt.Fprintf(out, "tty:%v %s\r\n", err == nil && fi.Mode()&os.ModeCharDevice != 0, size())
		flush()
	case "winch":
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGWINCH)
		fmt.Fprintf(out, "%s\r\n", size())
		flush()
		for {
			select {
			case <-ch:
				fmt.Fprintf(out, "%s\r\n", size())
				flush()
			case <-time.After(30 * time.Second):
				return
			}
		}
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
	case "ticker":
		for i := 1; i <= 100; i++ {
			fmt.Fprintf(out, "tick %d\r\n", i)
			flush()
			time.Sleep(100 * time.Millisecond)
		}
	case "flood":
		line := strings.Repeat("x", 78) + "\r\n"
		for i := 0; i < 260000; i++ { // about 20 MiB
			_, _ = out.WriteString(line)
		}
		fmt.Fprint(out, "flood-end\r\n")
		flush()
	}
}
