//go:build !windows

package proc

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startPTYHelper(t *testing.T, rows, cols uint16, replay int, extra ...string) *PTYProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p, err := StartPTY(ctx, PTYSpec{Spec: helperSpec(extra...), Rows: rows, Cols: cols, ReplayBytes: replay})
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}
	t.Cleanup(p.Cancel)
	return p
}

// waitOutput waits until the output seen so far (the replay plus what arrives
// live) contains want, and returns it all.
func waitOutput(t *testing.T, p *PTYProcess, want string) string {
	t.Helper()
	replay, live, cancel := p.Subscribe()
	defer cancel()
	got := string(replay)
	deadline := time.After(15 * time.Second)
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

func TestPTYRunsProgramOnATerminalOfTheRequestedSize(t *testing.T) {
	p := startPTYHelper(t, 30, 100, 0, "HELPER_MODE=tty")
	got := waitOutput(t, p, "size:")
	if !strings.Contains(got, "tty:true") || !strings.Contains(got, "size:30x100") {
		t.Fatalf("output = %q, want a terminal of 30 rows by 100 columns", got)
	}
	if exit := p.Wait(); exit.Code != 0 {
		t.Fatalf("exit = %+v", exit)
	}
	// Defaults when no size is given.
	q := startPTYHelper(t, 0, 0, 0, "HELPER_MODE=tty")
	if got := waitOutput(t, q, "size:"); !strings.Contains(got, "size:24x80") {
		t.Fatalf("default size: %q", got)
	}
}

func TestPTYResizeReachesProgram(t *testing.T) {
	p := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=winch")
	waitOutput(t, p, "size:24x80")
	if err := p.Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p, "size:40x120")
	p.Cancel()
	<-p.Done()
	if err := p.Resize(10, 10); err == nil {
		t.Fatal("resizing a finished terminal must fail")
	}
}

func TestPTYInputReachesProgram(t *testing.T) {
	p := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=readline")
	waitOutput(t, p, "ready")
	if _, err := p.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p, "got:hello")
	p.Cancel()
	<-p.Done()
	if _, err := p.Write([]byte("late\r")); err == nil {
		t.Fatal("writing to a finished terminal must fail")
	}
}

func TestPTYReplayThenLiveOutput(t *testing.T) {
	p := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=two")
	// Wait for the first line without subscribing: it must be in the replay.
	deadline := time.Now().Add(10 * time.Second)
	for !bytes.Contains(p.Replay(), []byte("first-line")) {
		if time.Now().After(deadline) {
			t.Fatal("first-line never reached the replay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	replay, live, cancel := p.Subscribe()
	defer cancel()
	if !bytes.Contains(replay, []byte("first-line")) || bytes.Contains(replay, []byte("second-line")) {
		t.Fatalf("replay = %q, want only what happened before the viewer joined", replay)
	}
	all := string(replay)
	for chunk := range live { // closes when the program ends
		all += string(chunk)
	}
	if strings.Count(all, "first-line") != 1 || strings.Count(all, "second-line") != 1 {
		t.Fatalf("replay + live = %q, want each line exactly once (nothing lost, nothing repeated)", all)
	}
	if strings.Index(all, "first-line") > strings.Index(all, "second-line") {
		t.Fatalf("out of order: %q", all)
	}
	// After the end, a late viewer still gets the replay and a closed channel.
	replay2, live2, _ := p.Subscribe()
	if !bytes.Contains(replay2, []byte("second-line")) {
		t.Fatalf("late replay = %q", replay2)
	}
	if _, ok := <-live2; ok {
		t.Fatal("the live channel of a finished terminal must be closed")
	}
}

func TestPTYSlowSubscriberDoesNotBlock(t *testing.T) {
	const keep = 64 * 1024
	p := startPTYHelper(t, 24, 80, keep, "HELPER_MODE=flood")

	_, slow, cancelSlow := p.Subscribe() // never read
	defer cancelSlow()
	_, fast, cancelFast := p.Subscribe()
	defer cancelFast()
	var received atomic.Int64
	go func() {
		for chunk := range fast {
			received.Add(int64(len(chunk)))
		}
	}()

	select {
	case <-p.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("a viewer that never reads stopped the program")
	}
	if exit := p.Wait(); exit.Code != 0 {
		t.Fatalf("exit = %+v", exit)
	}
	n := 0
	for range slow { // buffered chunks, then closed: it was disconnected
		n++
	}
	if n > subscriberBuffer {
		t.Fatalf("the slow viewer held %d chunks, more than its buffer (%d)", n, subscriberBuffer)
	}
	if received.Load() == 0 {
		t.Fatal("the fast viewer received nothing")
	}
	replay := p.Replay()
	if len(replay) != keep {
		t.Fatalf("replay is %d bytes, want exactly the %d kept", len(replay), keep)
	}
	if !bytes.Contains(replay, []byte("flood-end")) {
		t.Fatal("the replay must hold the most recent output, including the end")
	}
}

func TestPTYKeepsRunningWithoutViewers(t *testing.T) {
	p := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=ticker")
	time.Sleep(700 * time.Millisecond) // nobody is watching
	replay := string(p.Replay())
	if !strings.Contains(replay, "tick 5") {
		t.Fatalf("after 700 ms with no viewer the program should have reached tick 5+, replay = %q", replay)
	}
	select {
	case <-p.Done():
		t.Fatal("the program must keep running without viewers")
	default:
	}
}

func TestPTYCancelKillsWholeProcessTree(t *testing.T) {
	p := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=tree")
	out := waitOutput(t, p, "pids ")
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
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal did not finish after Cancel")
	}
	waitDead(t, leader, grandchild)
	if exit := p.Wait(); !exit.Canceled {
		t.Fatalf("exit = %+v, want Canceled", exit)
	}

	// Ctrl+C typed into the terminal ends a program that listens for it.
	q := startPTYHelper(t, 24, 80, 0, "HELPER_MODE=sleep")
	time.Sleep(300 * time.Millisecond)
	if err := q.Interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl+C should have ended the sleeping program")
	}
	if exit := q.Wait(); exit.Signal != "interrupt" {
		t.Fatalf("exit = %+v, want it ended by SIGINT", exit)
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
