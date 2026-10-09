package proc

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStartStreamsStdoutLines(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=lines")

	select {
	case l := <-p.Lines():
		if l.Stream != Stdout || l.Text != "one" {
			t.Fatalf("first line = %+v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no line within 5s")
	}
	select {
	case <-p.Done():
		t.Fatal("output must stream while the process runs, not arrive at exit")
	default:
	}

	rest := texts(collect(t, p, 5*time.Second), Stdout)
	if got := strings.Join(rest, ","); got != "two,three" {
		t.Fatalf("remaining lines = %q, want two,three in order", got)
	}
}

func TestStderrIsCapturedSeparately(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=both")
	got := collect(t, p, 5*time.Second)
	if out := texts(got, Stdout); len(out) != 1 || out[0] != "out" {
		t.Fatalf("stdout = %v", out)
	}
	if errs := texts(got, Stderr); len(errs) != 1 || errs[0] != "err" {
		t.Fatalf("stderr = %v", errs)
	}
}

func TestExitCodeIsReported(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=exit", "HELPER_CODE=3")
	collect(t, p, 5*time.Second)
	exit := p.Wait()
	if exit.Code != 3 || exit.Canceled || exit.Signal != "" {
		t.Fatalf("exit = %+v, want code 3, not canceled", exit)
	}
}

func TestCancelKillsWholeProcessTree(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=tree")
	leader, grandchild := readPids(t, p)
	if !alive(leader) || !alive(grandchild) {
		t.Fatalf("both processes must be running before the cancel (leader %d, grandchild %d)", leader, grandchild)
	}

	p.Cancel()
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("process did not finish after Cancel")
	}
	waitDead(t, leader, grandchild)
	if exit := p.Wait(); !exit.Canceled {
		t.Fatalf("exit = %+v, want Canceled", exit)
	}
}

func TestContextCancelKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Start(ctx, helperSpec("HELPER_MODE=tree"))
	if err != nil {
		t.Fatal(err)
	}
	leader, grandchild := readPids(t, p)
	cancel()
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("process did not finish after the context was cancelled")
	}
	waitDead(t, leader, grandchild)
}

func TestCancelEscalatesToKill(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=stubborn")
	select {
	case l := <-p.Lines():
		if l.Text != "ready" {
			t.Fatalf("got %q", l.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper never became ready")
	}
	p.Cancel() // the helper ignores SIGTERM; Grace is 300 ms
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("a process that ignores the polite request must still be killed after Grace")
	}
}

func TestLeaderExitKillsLeftovers(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=orphan")
	leader, grandchild := readPids(t, p)
	p.Wait()
	waitDead(t, leader, grandchild)
}

func TestEnvironmentIsExactlyWhatWasPassed(t *testing.T) {
	t.Setenv("LEAK_CANARY", "from-the-daemon")
	p := startHelper(t, "HELPER_MODE=env", "ONLY=this")

	var got []string
	for _, l := range collect(t, p, 5*time.Second) {
		if kv, ok := strings.CutPrefix(l.Text, "ENV:"); ok {
			if runtime.GOOS == "windows" && strings.HasPrefix(strings.ToUpper(kv), "SYSTEMROOT=") {
				continue // Windows cannot run a process without it
			}
			got = append(got, kv)
		}
	}
	sort.Strings(got)
	want := []string{"GO_WANT_HELPER=1", "HELPER_MODE=env", "ONLY=this"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("child environment = %v, want exactly %v", got, want)
	}
}

func TestRingBufferKeepsLastLines(t *testing.T) {
	spec := helperSpec("HELPER_MODE=many", "HELPER_COUNT=10")
	spec.RingLines = 3
	p, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p, 5*time.Second)
	p.Wait()

	var got []string
	for _, l := range p.Recent() {
		got = append(got, l.Text)
	}
	if want := "line 8,line 9,line 10"; strings.Join(got, ",") != want {
		t.Fatalf("Recent = %v, want %s", got, want)
	}
}

func TestMissingBinaryReturnsClearError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-cli")
	_, err := Start(context.Background(), Spec{Path: missing})
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("err = %v, want ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("the error must name the path %q, got %q", missing, err)
	}
	if _, err := Start(context.Background(), Spec{}); err == nil {
		t.Fatal("an empty path must be an error")
	}
}

func TestNoGoroutineLeakAfterExit(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		p, err := Start(context.Background(), helperSpec("HELPER_MODE=both"))
		if err != nil {
			t.Fatal(err)
		}
		collect(t, p, 5*time.Second)
		p.Wait()
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines grew from %d to %d:\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLongLineIsTruncated(t *testing.T) {
	spec := helperSpec("HELPER_MODE=long")
	spec.MaxLineBytes = 100
	p, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, p, 5*time.Second)
	if len(got) != 2 {
		t.Fatalf("lines = %+v, want the long line and \"after\"", got)
	}
	if len(got[0].Text) != 100 || !got[0].Truncated {
		t.Fatalf("long line: len %d truncated %v, want 100 and true", len(got[0].Text), got[0].Truncated)
	}
	if got[1].Text != "after" || got[1].Truncated {
		t.Fatalf("the line after a truncated one must be intact, got %+v", got[1])
	}
}

func TestStdinReachesChild(t *testing.T) {
	p := startHelper(t, "HELPER_MODE=echo")
	if _, err := p.Stdin().Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-p.Lines():
		if l.Text != "echo:hello" {
			t.Fatalf("got %q", l.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no echo from the child")
	}
	_ = p.Stdin().Close() // EOF ends the helper
	collect(t, p, 5*time.Second)
	if exit := p.Wait(); exit.Code != 0 {
		t.Fatalf("exit = %+v", exit)
	}
}

func TestInterruptReachesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		p := startHelper(t, "HELPER_MODE=sleep")
		if err := p.Interrupt(); !errors.Is(err, ErrInterruptUnsupported) {
			t.Fatalf("err = %v, want ErrInterruptUnsupported", err)
		}
		return
	}
	p := startHelper(t, "HELPER_MODE=sleep")
	time.Sleep(200 * time.Millisecond) // let the helper start
	if err := p.Interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("SIGINT should have ended the sleeping helper")
	}
	if exit := p.Wait(); exit.Signal != "interrupt" || exit.Canceled {
		t.Fatalf("exit = %+v, want signal interrupt and not canceled", exit)
	}
}

// readPids reads the "pids <leader> <grandchild>" line printed by the tree and
// orphan helpers.
func readPids(t *testing.T, p *Process) (leader, grandchild int) {
	t.Helper()
	select {
	case l, ok := <-p.Lines():
		if !ok {
			t.Fatalf("helper ended before printing its pids; recent output: %v", p.Recent())
		}
		f := strings.Fields(l.Text)
		if len(f) != 3 || f[0] != "pids" {
			t.Fatalf("unexpected first line %q", l.Text)
		}
		leader, _ = strconv.Atoi(f[1])
		grandchild, _ = strconv.Atoi(f[2])
		return leader, grandchild
	case <-time.After(10 * time.Second):
		t.Fatal("helper never printed its pids")
	}
	return 0, 0
}

// waitDead fails the test if any of the processes is still running after a
// few seconds.
func waitDead(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var running []int
		for _, pid := range pids {
			if alive(pid) {
				running = append(running, pid)
			}
		}
		if len(running) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes still running after the tree was killed: %v", running)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
