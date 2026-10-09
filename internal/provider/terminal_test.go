//go:build !windows

package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// terminalProfile is a profile whose "CLI" is the fake interactive program.
func terminalProfile(t *testing.T, l datadir.Layout, kind provider.Kind, sc providertest.Scenario) provider.Profile {
	t.Helper()
	p := fakeProfile(t, l, kind, string(kind), "in")
	p.ExtraEnv[providertest.EnvFakeScenario] = string(sc)
	return p
}

func launch(t *testing.T, s provider.Session, rows, cols uint16) provider.TerminalSession {
	t.Helper()
	ts, ok := s.(provider.TerminalSession)
	if !ok {
		t.Fatalf("%T is not a TerminalSession", s)
	}
	t.Cleanup(func() { _ = ts.Close() })
	if err := ts.Launch(context.Background(), rows, cols); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return ts
}

func startTerminal(t *testing.T, kind provider.Kind, p provider.Profile) provider.TerminalSession {
	t.Helper()
	req := provider.SessionRequest{Profile: p, Mode: provider.ModeTerminal, Dir: t.TempDir()}
	var s provider.Session
	var err error
	if kind == provider.KindClaude {
		s, err = provider.Claude{}.Start(context.Background(), req)
	} else {
		s, err = provider.Codex{}.Start(context.Background(), req)
	}
	if err != nil {
		t.Fatal(err)
	}
	return launch(t, s, 30, 100)
}

// seen collects terminal output until it contains want.
func seen(t *testing.T, ts provider.TerminalSession, want string) string {
	t.Helper()
	term := ts.Terminal()
	if term == nil {
		t.Fatal("the program is not running")
	}
	replay, live, cancel := term.Subscribe()
	defer cancel()
	got := string(replay)
	deadline := time.After(15 * time.Second)
	for !strings.Contains(got, want) {
		select {
		case chunk, ok := <-live:
			if !ok {
				t.Fatalf("the terminal closed before %q appeared; output: %q", want, got)
			}
			got += string(chunk)
		case <-deadline:
			t.Fatalf("%q never appeared; output: %q", want, got)
		}
	}
	return got
}

func nextEvent(t *testing.T, ts provider.TerminalSession) provider.Event {
	t.Helper()
	select {
	case ev, ok := <-ts.Events():
		if !ok {
			t.Fatal("the event stream is closed")
		}
		return ev
	case <-time.After(15 * time.Second):
		t.Fatal("no event")
	}
	return provider.Event{}
}

func waitDone(t *testing.T, ts provider.TerminalSession) provider.Done {
	t.Helper()
	for {
		if ev := nextEvent(t, ts); ev.Kind == provider.EventDone {
			return *ev.Done
		}
	}
}

func TestTerminalSessionHostsTheOfficialUI(t *testing.T) {
	l := layout(t)
	p := terminalProfile(t, l, provider.KindClaude, providertest.TUI)
	p.Mode = provider.ModeTerminal
	ts := startTerminal(t, provider.KindClaude, p)

	out := seen(t, ts, "ENV:TERM=") // the banner, the arguments and the environment
	for _, want := range []string{
		"FAKE-TUI ready",
		"BIN:" + p.Binary,       // exactly the installed binary
		"config=" + p.ConfigDir, // in the profile's isolated directory
		"ENV:TERM=xterm-256color",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	m := regexp.MustCompile(`ARGS:--session-id (\S+)`).FindStringSubmatch(out)
	if m == nil || !uuidPattern.MatchString(m[1]) {
		t.Fatalf("a new Claude Code session starts with --session-id <uuid>, args in output:\n%s", out)
	}
	if ts.ID() != m[1] {
		t.Errorf("ID() = %q, want the session ID we chose (%s)", ts.ID(), m[1])
	}
	if ev := nextEvent(t, ts); ev.Kind != provider.EventSession || ev.SessionID != m[1] {
		t.Errorf("first event = %+v, want the session ID", ev)
	}
	for _, line := range strings.Split(out, "\n") {
		if name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "ENV:"), "="); strings.HasPrefix(line, "ENV:") &&
			(strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_") || strings.Contains(name, "TOKEN") || strings.Contains(name, "SECRET")) {
			t.Errorf("environment variable %s would change the CLI's identity or leak a secret", name)
		}
	}

	// Codex CLI: no session ID of ours, a read-only sandbox, started in its own folder.
	cp := terminalProfile(t, l, provider.KindCodex, providertest.TUI)
	cp.Mode = provider.ModeTerminal
	cts := startTerminal(t, provider.KindCodex, cp)
	cout := seen(t, cts, "ENV:TERM=")
	if !strings.Contains(cout, "ARGS:-s read-only") || strings.Contains(cout, "--session-id") || !strings.Contains(cout, "config="+cp.ConfigDir) {
		t.Errorf("codex terminal launch:\n%s", cout)
	}
}

func TestTerminalSessionKeepsRunningWithoutViewer(t *testing.T) {
	p := terminalProfile(t, layout(t), provider.KindClaude, providertest.TUI)
	ts := startTerminal(t, provider.KindClaude, p)
	time.Sleep(700 * time.Millisecond) // nobody is attached
	select {
	case ev := <-ts.Events():
		if ev.Kind == provider.EventDone {
			t.Fatalf("the program stopped with no viewer: %+v", ev)
		}
	default:
	}
	if ts.Terminal() == nil {
		t.Fatal("the terminal must still be there")
	}
	// A viewer arriving now sees what happened meanwhile.
	if out := seen(t, ts, "FAKE-TUI ready"); !strings.Contains(out, "FAKE-TUI ready") {
		t.Fatal("the replay lost the banner")
	}
}

func TestTerminalSessionReplayThenLive(t *testing.T) {
	ts := startTerminal(t, provider.KindClaude, terminalProfile(t, layout(t), provider.KindClaude, providertest.TUI))
	seen(t, ts, "ENV:TERM=") // the banner is complete

	replay, live, cancel := ts.Terminal().Subscribe()
	defer cancel()
	if !strings.Contains(string(replay), "FAKE-TUI ready") {
		t.Fatalf("replay = %q, want the banner", replay)
	}
	if err := ts.Send(context.Background(), "hi there"); err != nil {
		t.Fatal(err)
	}
	var got string
	deadline := time.After(10 * time.Second)
	for !strings.Contains(got, "you said: hi there") {
		select {
		case chunk := <-live:
			got += string(chunk)
		case <-deadline:
			t.Fatalf("live output = %q", got)
		}
	}
	if strings.Contains(got, "FAKE-TUI ready") {
		t.Error("the live stream must not repeat what the replay already had")
	}
}

func TestTerminalSessionResizeAndInput(t *testing.T) {
	ts := startTerminal(t, provider.KindClaude, terminalProfile(t, layout(t), provider.KindClaude, providertest.TUI))
	seen(t, ts, "ENV:TERM=")
	if err := ts.Send(context.Background(), "/size"); err != nil {
		t.Fatal(err)
	}
	seen(t, ts, "size:30x100") // the size it was launched with
	if err := ts.Terminal().Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	if err := ts.Send(context.Background(), "/size"); err != nil {
		t.Fatal(err)
	}
	seen(t, ts, "size:40x120")
}

func TestTerminalSessionInterruptAndCancel(t *testing.T) {
	p := terminalProfile(t, layout(t), provider.KindClaude, providertest.TUI)
	ts := startTerminal(t, provider.KindClaude, p)
	first := nextEvent(t, ts) // the session ID
	id := first.SessionID
	seen(t, ts, "ENV:TERM=")

	if err := ts.Launch(context.Background(), 24, 80); err == nil {
		t.Fatal("launching a running program must fail")
	}

	// Ctrl+C ends this program; that is a cancel, not a failure.
	if err := ts.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if d := waitDone(t, ts); d.Reason != provider.DoneCanceled {
		t.Fatalf("after Ctrl+C: %+v, want canceled", d)
	}
	if ts.Terminal() != nil {
		t.Fatal("no terminal once the program ended")
	}
	if err := ts.Send(context.Background(), "x"); err == nil {
		t.Fatal("sending with no program running must fail")
	}

	// Launching again resumes the same provider session.
	if err := ts.Launch(context.Background(), 24, 80); err != nil {
		t.Fatal(err)
	}
	if out := seen(t, ts, "ENV:TERM="); !strings.Contains(out, "ARGS:--resume "+id) {
		t.Fatalf("the relaunch must resume %s:\n%s", id, out)
	}

	if err := ts.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	if d := waitDone(t, ts); d.Reason != provider.DoneCanceled {
		t.Fatalf("after cancel: %+v", d)
	}

	// A program that ends by itself is a completed session.
	if err := ts.Launch(context.Background(), 24, 80); err != nil {
		t.Fatal(err)
	}
	seen(t, ts, "ENV:TERM=")
	if err := ts.Send(context.Background(), "/exit"); err != nil {
		t.Fatal(err)
	}
	if d := waitDone(t, ts); d.Reason != provider.DoneCompleted {
		t.Fatalf("after /exit: %+v, want completed", d)
	}

	if err := ts.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ts.Launch(context.Background(), 24, 80); err == nil {
		t.Fatal("launching after Close must fail")
	}
}

func TestClaudeDefaultsToTerminalMode(t *testing.T) {
	l := layout(t)
	p := terminalProfile(t, l, provider.KindClaude, providertest.TUI)
	p.Mode = "" // nothing chosen
	s, err := provider.Claude{}.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, ok := s.(provider.TerminalSession); !ok || s.Mode() != provider.ModeTerminal {
		t.Fatalf("a Claude Code profile with no mode must run in terminal mode, got %T / %s", s, s.Mode())
	}
	d, err := provider.Claude{}.Detect(context.Background(), p)
	if err != nil || len(d.Modes) < 2 || d.Modes[0] != provider.ModeTerminal {
		t.Fatalf("modes = %v, want terminal first (it is the default), %v", d.Modes, err)
	}
	// Structured mode is still behind its notice.
	if _, err := (provider.Claude{}).Start(context.Background(), provider.SessionRequest{Profile: p, Mode: provider.ModeStructured}); err == nil {
		t.Fatal("structured mode must still require the accepted notice")
	}
	// Codex stays structured by default.
	cp := terminalProfile(t, l, provider.KindCodex, providertest.Hello)
	cp.Mode = ""
	cs, err := provider.Codex{}.Start(context.Background(), provider.SessionRequest{Profile: cp, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	if cs.Mode() != provider.ModeStructured {
		t.Fatalf("Codex defaults to %s, want structured", cs.Mode())
	}
	if _, err := (provider.Claude{}).Start(context.Background(), provider.SessionRequest{Profile: p, Mode: "carrier-pigeon"}); err == nil {
		t.Fatal("an unknown mode must be refused")
	}
}

func TestLoginFlowRunsInsideTheProfile(t *testing.T) {
	l := layout(t)
	ctx := context.Background()
	for _, kind := range []provider.Kind{provider.KindClaude, provider.KindCodex} {
		t.Run(string(kind), func(t *testing.T) {
			target := fakeProfile(t, l, kind, "target "+string(kind), "out")
			bystander := fakeProfile(t, l, kind, "bystander "+string(kind), "out")

			check := func(p provider.Profile) provider.LoginState {
				t.Helper()
				login, err := provider.CheckLogin(ctx, p, noEnv)
				if err != nil {
					t.Fatal(err)
				}
				return login.State
			}
			if check(target) != provider.LoginLoggedOut {
				t.Fatal("the profile starts logged out")
			}

			var s provider.TerminalSession
			var err error
			if kind == provider.KindClaude {
				s, err = provider.Claude{}.LoginSession(target)
			} else {
				s, err = provider.Codex{}.LoginSession(target)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if err := s.Launch(ctx, 24, 80); err != nil {
				t.Fatal(err)
			}
			seen(t, s, "Press Enter to log in")
			if _, err := s.Terminal().Write([]byte("\r")); err != nil {
				t.Fatal(err)
			}
			if d := waitDone(t, s); d.Reason != provider.DoneCompleted {
				t.Fatalf("login ended with %+v", d)
			}

			if check(target) != provider.LoginLoggedIn {
				t.Fatal("after the login flow the profile must be logged in")
			}
			if check(bystander) != provider.LoginLoggedOut {
				t.Fatal("logging one profile in must not log another one in: they share nothing")
			}
			if b, _ := os.ReadFile(filepath.Join(target.ConfigDir, "fake-login")); string(b) != "in" {
				t.Fatalf("the login must be written inside the profile's own directory, found %q", b)
			}
		})
	}
}

func TestTerminalModeNeverReadsCredentialFiles(t *testing.T) {
	p := terminalProfile(t, layout(t), provider.KindClaude, providertest.TUI)
	canary := filepath.Join(p.ConfigDir, ".credentials.json")
	if err := os.WriteFile(canary, []byte(`{"token":"CANARY-DO-NOT-READ"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(canary, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(canary, 0o600) })

	ts := startTerminal(t, provider.KindClaude, p)
	out := seen(t, ts, "ENV:TERM=")
	if strings.Contains(out, "CANARY") {
		t.Fatal("credential content reached the terminal output")
	}
	assertProviderNeverOpensFiles(t)
}
