// Package providertest has what the tests of providers and of the
// orchestrator need: a fake provider that runs a scripted fake CLI as a real
// supervised process, and the contract test suite every provider must pass.
//
// The fake CLI is the test binary itself: a test package calls
// MaybeRunFakeCLI first thing in TestMain, and the fake provider starts the
// test binary again with AOTUS_FAKE_CLI=1.
package providertest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

// EnvFakeCLI switches the test binary into fake CLI mode.
const EnvFakeCLI = "AOTUS_FAKE_CLI"

// Scenario selects what the fake CLI does.
type Scenario string

// Scenarios understood by the fake CLI.
const (
	// Hello answers "pong" (or "resumed:<id>" when resuming) and completes.
	Hello Scenario = "hello"
	// Fail reports a login problem and exits with code 1 without a done event.
	Fail Scenario = "fail"
	// Tree starts a grandchild process, records both PIDs, and then blocks.
	Tree Scenario = "tree"
	// Sleep blocks for a minute.
	Sleep Scenario = "sleep"
	// EnvDump prints its whole environment as text.
	EnvDump Scenario = "envdump"
	// Stdin waits for the end of its stdin and then says so.
	Stdin Scenario = "stdin"
	// Interruptible blocks until SIGINT, then reports an error and ends.
	Interruptible Scenario = "interruptible"
)

// MaybeRunFakeCLI runs the fake CLI and exits when the process was started as
// one; otherwise it returns immediately. Call it first in TestMain.
func MaybeRunFakeCLI() {
	if os.Getenv(EnvFakeCLI) != "1" {
		return
	}
	runFakeCLI(os.Args[1:])
	os.Exit(0)
}

type fakeLine map[string]any

func runFakeCLI(args []string) {
	opts := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		opts[strings.TrimPrefix(args[i], "--")] = args[i+1]
	}
	out := bufio.NewWriter(os.Stdout)
	emit := func(l fakeLine) {
		b, _ := json.Marshal(l)
		_, _ = out.Write(append(b, '\n'))
		_ = out.Flush()
	}
	session := "fake-session-1"
	if r := opts["resume"]; r != "" {
		session = r
	}

	switch Scenario(opts["scenario"]) {
	case Hello:
		emit(fakeLine{"type": "session", "id": session})
		if opts["resume"] != "" {
			emit(fakeLine{"type": "text", "text": "resumed:" + opts["resume"]})
		} else {
			emit(fakeLine{"type": "text", "text": "po"})
			emit(fakeLine{"type": "text", "text": "ng"})
		}
		emit(fakeLine{"type": "done", "reason": "completed", "input_tokens": 3, "output_tokens": 2})
	case Fail:
		emit(fakeLine{"type": "error", "code": "needs_login", "message": "please log in again"})
		os.Exit(1)
	case Tree:
		emit(fakeLine{"type": "session", "id": session})
		emit(fakeLine{"type": "text", "text": "working"})
		attr := &os.ProcAttr{
			Env:   []string{EnvFakeCLI + "=1", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")},
			Files: []*os.File{nil, nil, nil},
		}
		child, err := os.StartProcess(os.Args[0], []string{os.Args[0], "--scenario", string(Sleep)}, attr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cli: cannot start grandchild:", err)
			os.Exit(2)
		}
		if pf := opts["pidfile"]; pf != "" {
			_ = os.WriteFile(pf, []byte(strconv.Itoa(os.Getpid())+" "+strconv.Itoa(child.Pid)), 0o600)
		}
		time.Sleep(time.Minute)
	case Sleep:
		time.Sleep(time.Minute)
	case EnvDump:
		emit(fakeLine{"type": "session", "id": session})
		for _, kv := range os.Environ() {
			emit(fakeLine{"type": "text", "text": "ENV:" + kv})
		}
		emit(fakeLine{"type": "done", "reason": "completed"})
	case Stdin:
		_, _ = bufio.NewReader(os.Stdin).ReadString(0) // returns at EOF
		emit(fakeLine{"type": "text", "text": "stdin-eof"})
		emit(fakeLine{"type": "done", "reason": "completed"})
	case Interruptible:
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		emit(fakeLine{"type": "session", "id": session})
		emit(fakeLine{"type": "text", "text": "working"})
		select {
		case <-sig:
			emit(fakeLine{"type": "error", "code": "cli_error", "message": "interrupted"})
			emit(fakeLine{"type": "done", "reason": "failed"})
		case <-time.After(time.Minute):
		}
	default:
		fmt.Fprintln(os.Stderr, "fake cli: unknown scenario", opts["scenario"])
		os.Exit(2)
	}
}
