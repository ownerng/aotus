//go:build windows

package proc

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These tests need Windows 10 1809 or newer (ConPTY). Their result, with the
// OS version, is recorded in docs/research/windows-verification.md (P1-021).

func startWinPTY(t *testing.T, extra ...string) *PTYProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := StartPTY(ctx, PTYSpec{Spec: helperSpec(extra...), Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}
	t.Cleanup(p.Cancel)
	return p
}

func winOutput(t *testing.T, p *PTYProcess, want string) string {
	t.Helper()
	replay, live, cancel := p.Subscribe()
	defer cancel()
	got := string(replay)
	deadline := time.After(20 * time.Second)
	for !strings.Contains(got, want) {
		select {
		case chunk, ok := <-live:
			if !ok {
				t.Fatalf("the terminal closed before %q appeared; output so far: %q", want, got)
			}
			got += string(chunk)
		case <-deadline:
			t.Fatalf("%q never appeared; output so far: %q", want, got)
		}
	}
	return got
}

func TestPTYWindowsRunsProgramAndEnds(t *testing.T) {
	if !PTYSupported() {
		t.Skip("no ConPTY here")
	}
	p := startWinPTY(t, "HELPER_MODE=two")
	winOutput(t, p, "first-line")
	winOutput(t, p, "second-line")
	select {
	case <-p.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("the terminal did not finish after the program ended")
	}
	if exit := p.Wait(); exit.Code != 0 {
		t.Fatalf("exit = %+v", exit)
	}
}

func TestPTYWindowsInputReachesProgram(t *testing.T) {
	if !PTYSupported() {
		t.Skip("no ConPTY here")
	}
	p := startWinPTY(t, "HELPER_MODE=readline")
	winOutput(t, p, "ready")
	if _, err := p.Write([]byte("hello\r\n")); err != nil {
		t.Fatal(err)
	}
	winOutput(t, p, "got:hello")
	if err := p.Resize(40, 120); err != nil {
		t.Fatalf("Resize: %v", err)
	}
}

func TestPTYWindowsCancelKillsWholeProcessTree(t *testing.T) {
	if !PTYSupported() {
		t.Skip("no ConPTY here")
	}
	p := startWinPTY(t, "HELPER_MODE=tree")
	out := winOutput(t, p, "pids ")
	var leader, grandchild int
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(strings.TrimSpace(line)); len(f) == 3 && f[0] == "pids" {
			leader, grandchild = atoi(f[1]), atoi(f[2])
		}
	}
	if leader == 0 || grandchild == 0 || !alive(leader) || !alive(grandchild) {
		t.Fatalf("both processes must be running first (leader %d, grandchild %d) in %q", leader, grandchild, out)
	}
	p.Cancel()
	select {
	case <-p.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("the terminal did not finish after Cancel")
	}
	waitDead(t, leader, grandchild)
	if exit := p.Wait(); !exit.Canceled {
		t.Fatalf("exit = %+v, want Canceled", exit)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
