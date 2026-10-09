// Package providertest has what the tests of providers and of the
// orchestrator need: a fake provider that runs a scripted fake CLI as a real
// supervised process, and the contract test suite every provider must pass.
//
// The fake CLI is the test binary itself: a test package calls
// MaybeRunFakeCLI first thing in TestMain, and the fake provider starts the
// test binary again with AOTUS_FAKE_CLI=1.
//
// The fake CLI can imitate the output of Claude Code (it is started with -p
// like the real one) so that the real adapter is tested end to end.
package providertest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EnvFakeCLI switches the test binary into fake CLI mode.
const EnvFakeCLI = "AOTUS_FAKE_CLI"

// Environment variables a profile passes (through ExtraEnv) to select what an
// imitation of a real CLI does, since the adapter owns the command line.
const (
	EnvFakeScenario = "AOTUS_FAKE_SCENARIO"
	EnvFakePidFile  = "AOTUS_FAKE_PIDFILE"
	// EnvFakeReplay is a file whose lines the Replay scenario prints verbatim.
	EnvFakeReplay = "AOTUS_FAKE_REPLAY"
)

// Scenario selects what the fake CLI does.
type Scenario string

// Scenarios understood by the fake CLI.
const (
	// Hello answers "pong" (or "resumed:<id>" when resuming) and completes.
	Hello Scenario = "hello"
	// Fail reports a login problem and exits with code 1.
	Fail Scenario = "fail"
	// Tree starts a grandchild process, records both PIDs, and then blocks.
	Tree Scenario = "tree"
	// Sleep blocks for a minute.
	Sleep Scenario = "sleep"
	// EnvDump prints its whole environment as text.
	EnvDump Scenario = "envdump"
	// Stdin waits for the end of its stdin and then says so.
	Stdin Scenario = "stdin"
	// Interruptible blocks until SIGINT, then reports it and ends.
	Interruptible Scenario = "interruptible"
	// Replay prints the file named by EnvFakeReplay (a recorded stream).
	Replay Scenario = "replay"
	// Argv reports how the CLI was started: binary, arguments, environment and
	// what arrived on stdin.
	Argv Scenario = "argv"
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

// configDir is where a CLI keeps its state: the fake uses the same variables
// as the real ones.
func configDir() (dir, defaultVersion string) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, "2.1.295 (Claude Code)"
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d, "codex-cli 0.160.0"
	}
	return "", "fake 1.0"
}

// fakeInfo answers --version, "auth status" (Claude Code style JSON) and
// "login status" (Codex CLI style text). Like the real CLIs, the fake keeps
// its state inside its configuration directory: the files "fake-login" ("in"
// or "out") and "fake-version".
func fakeInfo(args []string) bool {
	dir, version := configDir()
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	switch {
	case len(args) == 1 && args[0] == "--version":
		if v := read("fake-version"); v != "" {
			version = v
		}
		fmt.Println(version)
		return true
	case len(args) == 2 && args[1] == "status" && (args[0] == "auth" || args[0] == "login"):
		loggedIn := read("fake-login") == "in"
		switch {
		case args[0] == "auth" && loggedIn:
			fmt.Println(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"someone@example.com","orgName":"Example","subscriptionType":"pro"}`)
		case args[0] == "auth":
			fmt.Println(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
		case loggedIn:
			// Like the real Codex CLI: the status goes to stderr, after a warning.
			fmt.Fprintln(os.Stderr, "WARNING: proceeding, even though we could not create PATH aliases")
			fmt.Fprintln(os.Stderr, "Logged in using ChatGPT")
		default:
			fmt.Fprintln(os.Stderr, "Not logged in")
			os.Exit(1) // the real one exits 1 when logged out
		}
		return true
	}
	return false
}

// output writes what a CLI prints, in one dialect.
type output interface {
	session(id string)
	text(s string)
	failure(code, msg string)
	done()
	interrupted()
}

func runFakeCLI(args []string) {
	if fakeInfo(args) {
		return
	}

	var (
		out       output
		scenario  Scenario
		pidfile   string
		resume    string
		stdinText []byte
	)
	w := bufio.NewWriter(os.Stdout)
	switch {
	case len(args) > 0 && args[0] == "-p": // imitating Claude Code
		out = claudeOutput{w: w}
		scenario = Scenario(os.Getenv(EnvFakeScenario))
		pidfile = os.Getenv(EnvFakePidFile)
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--resume" {
				resume = args[i+1]
			}
		}
		// The real CLI reads the prompt from stdin: a runner that did not
		// close stdin would hang here, as it would with the real thing.
		stdinText, _ = io.ReadAll(os.Stdin)
	case len(args) > 0 && args[0] == "exec": // imitating Codex CLI
		out = codexOutput{w: w}
		scenario = Scenario(os.Getenv(EnvFakeScenario))
		pidfile = os.Getenv(EnvFakePidFile)
		if len(args) > 2 && args[1] == "resume" {
			resume = args[len(args)-2] // `exec resume ... <id> -`
		}
		stdinText, _ = io.ReadAll(os.Stdin)
	default:
		out = plainOutput{w: w}
		opts := map[string]string{}
		for i := 0; i+1 < len(args); i += 2 {
			opts[strings.TrimPrefix(args[i], "--")] = args[i+1]
		}
		scenario, pidfile, resume = Scenario(opts["scenario"]), opts["pidfile"], opts["resume"]
	}

	session := "fake-session-1"
	if resume != "" {
		session = resume
	}

	switch scenario {
	case Hello:
		out.session(session)
		if resume != "" {
			out.text("resumed:" + resume)
		} else {
			out.text("po")
			out.text("ng")
		}
		out.done()
	case Fail:
		out.failure("needs_login", "Please run /login: you are not logged in")
		os.Exit(1)
	case Tree:
		out.session(session)
		out.text("working")
		attr := &os.ProcAttr{
			Env:   []string{EnvFakeCLI + "=1", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")},
			Files: []*os.File{nil, nil, nil},
		}
		// os.StartProcess keeps the grandchild in this process's group, like
		// the helpers a real CLI starts.
		child, err := os.StartProcess(os.Args[0], []string{os.Args[0], "--scenario", string(Sleep)}, attr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cli: cannot start grandchild:", err)
			os.Exit(2)
		}
		if pidfile != "" {
			_ = os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())+" "+strconv.Itoa(child.Pid)), 0o600)
		}
		time.Sleep(time.Minute)
	case Sleep:
		time.Sleep(time.Minute)
	case EnvDump:
		out.session(session)
		for _, kv := range os.Environ() {
			out.text("ENV:" + kv)
		}
		out.done()
	case Replay:
		b, err := os.ReadFile(os.Getenv(EnvFakeReplay))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cli: cannot read the recording:", err)
			os.Exit(2)
		}
		_, _ = w.Write(b)
		_ = w.Flush()
	case Argv:
		out.session(session)
		out.text("BIN:" + os.Args[0])
		for _, a := range args {
			out.text("ARG:" + a)
		}
		for _, kv := range os.Environ() {
			out.text("ENV:" + kv)
		}
		out.text("STDIN:" + string(stdinText))
		out.done()
	case Stdin:
		_, _ = bufio.NewReader(os.Stdin).ReadString(0) // returns at EOF
		out.text("stdin-eof")
		out.done()
	case Interruptible:
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		out.session(session)
		out.text("working")
		select {
		case <-sig:
			out.interrupted()
		case <-time.After(time.Minute):
		}
	default:
		fmt.Fprintln(os.Stderr, "fake cli: unknown scenario", scenario)
		os.Exit(2)
	}
}

type line map[string]any

func write(w *bufio.Writer, l line) {
	b, _ := json.Marshal(l)
	_, _ = w.Write(append(b, '\n'))
	_ = w.Flush()
}

// plainOutput is the dialect of the fake provider.
type plainOutput struct{ w *bufio.Writer }

func (o plainOutput) session(id string) { write(o.w, line{"type": "session", "id": id}) }
func (o plainOutput) text(s string)     { write(o.w, line{"type": "text", "text": s}) }
func (o plainOutput) failure(code, msg string) {
	write(o.w, line{"type": "error", "code": code, "message": msg})
}
func (o plainOutput) done() {
	write(o.w, line{"type": "done", "reason": "completed", "input_tokens": 3, "output_tokens": 2})
}
func (o plainOutput) interrupted() {
	write(o.w, line{"type": "error", "code": "cli_error", "message": "interrupted"})
	write(o.w, line{"type": "done", "reason": "failed"})
}

// codexOutput imitates `codex exec --json`.
type codexOutput struct{ w *bufio.Writer }

func (o codexOutput) session(id string) {
	write(o.w, line{"type": "thread.started", "thread_id": id})
	write(o.w, line{"type": "turn.started"})
}

func (o codexOutput) text(s string) {
	write(o.w, line{"type": "item.completed", "item": line{"id": "item_" + strconv.Itoa(len(s)), "type": "agent_message", "text": s}})
}

func (o codexOutput) failure(_, msg string) {
	inner, _ := json.Marshal(line{"type": "error", "status": 401, "error": line{"message": "Your authentication token has expired. Please try refreshing it. (" + msg + ")"}})
	write(o.w, line{"type": "error", "message": string(inner)})
	write(o.w, line{"type": "turn.failed", "error": line{"message": string(inner)}})
}

func (o codexOutput) done() {
	write(o.w, line{"type": "turn.completed", "usage": line{"input_tokens": 3, "output_tokens": 2}})
}

func (o codexOutput) interrupted() {
	write(o.w, line{"type": "turn.failed", "error": line{"message": "interrupted"}})
}

// claudeOutput imitates the stream-json output of Claude Code.
type claudeOutput struct{ w *bufio.Writer }

const fakeClaudeSession = "fake-session-1"

func (o claudeOutput) session(id string) {
	write(o.w, line{"type": "system", "subtype": "init", "session_id": id, "model": "fake-model", "cwd": "/work"})
}

func (o claudeOutput) text(s string) {
	write(o.w, line{
		"type": "stream_event", "session_id": fakeClaudeSession, "api_message_id": "msg_fake",
		"event": line{"type": "content_block_delta", "index": 0, "delta": line{"type": "text_delta", "text": s}},
	})
}

func (o claudeOutput) failure(_, msg string) {
	write(o.w, line{"type": "result", "subtype": "success", "is_error": true, "result": msg, "session_id": fakeClaudeSession})
}

func (o claudeOutput) done() {
	write(o.w, line{
		"type":            "rate_limit_event",
		"rate_limit_info": line{"status": "allowed", "resetsAt": 1791526200, "rateLimitType": "five_hour"},
	})
	write(o.w, line{
		"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed",
		"result": "ok", "session_id": fakeClaudeSession, "total_cost_usd": 0.01,
		"usage": line{"input_tokens": 3, "output_tokens": 2},
	})
}

func (o claudeOutput) interrupted() {
	write(o.w, line{
		"type": "result", "subtype": "error_during_execution", "is_error": true,
		"terminal_reason": "aborted_streaming", "session_id": fakeClaudeSession,
	})
}
