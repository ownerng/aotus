package proc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests re-execute the test binary as the child process. TestHelperProcess
// is the child's main: it does nothing unless GO_WANT_HELPER=1.
//
// Modes (HELPER_MODE):
//
//	lines      print "one", "two", "three" to stdout with a pause between them
//	both       print "out" to stdout and "err" to stderr
//	exit       exit with the code in HELPER_CODE
//	env        print every environment variable as "ENV:KEY=VALUE"
//	many       print HELPER_COUNT numbered lines
//	long       print one very long line and then "after"
//	tree       start a grandchild that sleeps, print both PIDs, then sleep
//	orphan     like tree, but the leader exits and leaves the grandchild behind
//	stubborn   ignore SIGTERM and sleep (tests escalation to kill)
//	sleep      sleep for a minute
//	echo       copy stdin to stdout, line by line
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		return
	}
	out := bufio.NewWriter(os.Stdout)
	flush := func() { _ = out.Flush() }
	switch os.Getenv("HELPER_MODE") {
	case "lines":
		for _, s := range []string{"one", "two", "three"} {
			fmt.Fprintln(out, s)
			flush()
			time.Sleep(150 * time.Millisecond)
		}
	case "both":
		fmt.Fprintln(os.Stdout, "out")
		fmt.Fprintln(os.Stderr, "err")
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("HELPER_CODE"))
		os.Exit(code)
	case "env":
		for _, kv := range os.Environ() {
			fmt.Fprintln(out, "ENV:"+kv)
		}
		flush()
	case "many":
		n, _ := strconv.Atoi(os.Getenv("HELPER_COUNT"))
		for i := 1; i <= n; i++ {
			fmt.Fprintf(out, "line %d\n", i)
		}
		flush()
	case "long":
		fmt.Fprintln(out, strings.Repeat("x", 5000))
		fmt.Fprintln(out, "after")
		flush()
	case "tree", "orphan":
		// A plain exec child stays in this process's group, like the
		// grandchildren a real CLI starts.
		child := exec.Command(os.Args[0], "-test.run=TestHelperProcess") //nolint:noctx // the helper must outlive any context to test tree cleanup
		child.Env = helperEnv("HELPER_MODE=sleep")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "cannot start grandchild:", err)
			os.Exit(2)
		}
		fmt.Fprintf(out, "pids %d %d\n", os.Getpid(), child.Process.Pid)
		flush()
		if os.Getenv("HELPER_MODE") == "orphan" {
			os.Exit(0) // leave the grandchild behind
		}
		time.Sleep(time.Minute)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		fmt.Fprintln(out, "ready")
		flush()
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	case "echo":
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Fprintln(out, "echo:"+sc.Text())
			flush()
		}
	default:
		runTerminalHelper(os.Getenv("HELPER_MODE"), out)
	}
	os.Exit(0)
}

// helperEnv is the exact environment for a helper child.
func helperEnv(extra ...string) []string {
	env := []string{"GO_WANT_HELPER=1"}
	if runtime.GOOS == "windows" {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	return append(env, extra...)
}

func helperSpec(extra ...string) Spec {
	return Spec{
		Path:  os.Args[0],
		Args:  []string{"-test.run=TestHelperProcess"},
		Env:   helperEnv(extra...),
		Grace: 300 * time.Millisecond,
	}
}

func startHelper(t *testing.T, extra ...string) *Process {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := Start(ctx, helperSpec(extra...))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(p.Cancel)
	return p
}

// collect drains Lines until it is closed or the timeout passes.
func collect(t *testing.T, p *Process, timeout time.Duration) []Line {
	t.Helper()
	var got []Line
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-p.Lines():
			if !ok {
				return got
			}
			got = append(got, l)
		case <-deadline:
			t.Fatalf("timed out after %s; lines so far: %v", timeout, got)
		}
	}
}

func texts(ls []Line, s Stream) []string {
	var out []string
	for _, l := range ls {
		if l.Stream == s {
			out = append(out, l.Text)
		}
	}
	return out
}
