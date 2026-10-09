package provider_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"aotus/internal/datadir"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
)

// claudeProfile is a Claude Code profile whose "CLI" is the fake one imitating
// Claude Code's output.
func claudeProfile(t *testing.T, l datadir.Layout, sc providertest.Scenario) provider.Profile {
	t.Helper()
	p := fakeProfile(t, l, provider.KindClaude, "claude", "in")
	p.ExtraEnv[providertest.EnvFakeScenario] = string(sc)
	p.ExtraEnv[providertest.EnvFakePidFile] = filepath.Join(t.TempDir(), "pids")
	p.Mode = provider.ModeStructured
	p.AcceptedNotices = []string{provider.NoticeClaudeHeadless}
	return p
}

func startClaude(t *testing.T, p provider.Profile) provider.Session {
	t.Helper()
	s, err := provider.Claude{}.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func claudeContract() providertest.Contract {
	return providertest.Contract{
		Process: true,
		New: func(t *testing.T, sc providertest.Scenario) (provider.Session, providertest.Probe) {
			t.Helper()
			p := claudeProfile(t, layout(t), sc)
			return startClaude(t, p), providertest.Probe{PidFile: p.ExtraEnv[providertest.EnvFakePidFile]}
		},
	}
}

func TestClaudePassesContract(t *testing.T) {
	providertest.RunContract(t, claudeContract())
}

// runTurn sends one prompt and returns the events of the turn.
func runTurn(t *testing.T, s provider.Session, prompt string) []provider.Event {
	t.Helper()
	if err := s.Send(context.Background(), prompt); err != nil {
		t.Fatal(err)
	}
	var events []provider.Event
	for ev := range s.Events() {
		events = append(events, ev)
		if ev.Kind == provider.EventDone {
			return events
		}
	}
	t.Fatal("event stream closed before the turn ended")
	return nil
}

func eventsText(events []provider.Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Kind == provider.EventText {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// The recordings are real output of Claude Code 2.1.295 (testdata/README.md),
// replayed through the real adapter and the real process supervision.
func TestClaudeParsesRecordedStream(t *testing.T) {
	replay := func(file string) []provider.Event {
		p := claudeProfile(t, layout(t), providertest.Replay)
		abs, err := filepath.Abs(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		p.ExtraEnv[providertest.EnvFakeReplay] = abs
		return runTurn(t, startClaude(t, p), "ping")
	}

	events := replay("claude-stream.jsonl")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("the recorded turn violates the event contract: %v\n%v", err, events)
	}
	if got := eventsText(events); got != "pong" {
		t.Fatalf("answer = %q, want pong", got)
	}
	var session, limits bool
	for _, e := range events {
		switch e.Kind {
		case provider.EventSession:
			session = e.SessionID != ""
		case provider.EventLimits:
			limits = e.Limits.Status == "allowed" && e.Limits.Window == "five_hour" && !e.Limits.ResetsAt.IsZero()
		}
	}
	if !session || !limits {
		t.Fatalf("session reported: %v, plan limits reported: %v; both are in the recording", session, limits)
	}
	done := events[len(events)-1].Done
	if done.Reason != provider.DoneCompleted || done.CostUSD <= 0 || done.OutputTokens == 0 {
		t.Fatalf("done = %+v, want completed with cost and tokens", done)
	}

	interrupted := replay("claude-interrupted.jsonl")
	if err := provider.ValidateTurn(interrupted); err != nil {
		t.Fatalf("the interrupted turn violates the contract: %v\n%v", err, interrupted)
	}
	last := interrupted[len(interrupted)-1].Done
	if last.Reason != provider.DoneCanceled {
		t.Fatalf("an interrupted turn must end as canceled, got %+v", last)
	}
	for _, e := range interrupted {
		if e.Kind == provider.EventError {
			t.Fatalf("an interrupt is not an error: %+v", e)
		}
	}
}

// A real run of a logged-out profile: the failure notice must arrive as one
// needs_login error, not also as assistant text.
func TestClaudeLoggedOutRecording(t *testing.T) {
	p := claudeProfile(t, layout(t), providertest.Replay)
	abs, err := filepath.Abs(filepath.Join("testdata", "claude-logged-out.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p.ExtraEnv[providertest.EnvFakeReplay] = abs
	events := runTurn(t, startClaude(t, p), "hi")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("%v\n%v", err, events)
	}
	if got := eventsText(events); got != "" {
		t.Fatalf("the failure notice %q must not be shown as an answer", got)
	}
	var errs []provider.Event
	for _, e := range events {
		if e.Kind == provider.EventError {
			errs = append(errs, e)
		}
	}
	if len(errs) != 1 || errs[0].Code != provider.CodeNeedsLogin || !strings.Contains(errs[0].Text, "/login") {
		t.Fatalf("errors = %+v, want exactly one needs_login error that tells the user to log in", errs)
	}
	if events[len(events)-1].Done.Reason != provider.DoneFailed {
		t.Fatalf("done = %+v, want failed", events[len(events)-1].Done)
	}
}

func TestClaudeReportsMissingLoggedOutAndOldCLI(t *testing.T) {
	ctx := context.Background()
	l := layout(t)
	c := provider.Claude{}

	ok := claudeProfile(t, l, providertest.Hello)
	d, err := c.Detect(ctx, ok)
	if err != nil || !d.Installed || !d.VersionOK || d.Login != provider.LoginLoggedIn || d.Version != "2.1.295" {
		t.Fatalf("a healthy CLI: %+v, %v", d, err)
	}
	if err := c.Preflight(ctx, ok); err != nil {
		t.Fatalf("a healthy CLI must pass the preflight: %v", err)
	}

	missing := ok
	missing.Binary = filepath.Join(t.TempDir(), "no-claude-here")
	loggedOut := claudeProfile(t, l, providertest.Hello)
	if err := os.WriteFile(filepath.Join(loggedOut.ConfigDir, "fake-login"), []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := claudeProfile(t, l, providertest.Hello)
	if err := os.WriteFile(filepath.Join(old.ConfigDir, "fake-version"), []byte("2.0.1 (Claude Code)"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		p    provider.Profile
		want error
	}{
		{"missing CLI", missing, provider.ErrCLINotInstalled},
		{"logged-out CLI", loggedOut, provider.ErrNeedsLogin},
		{"old CLI", old, provider.ErrUnsupportedVersion},
	}
	all := []error{provider.ErrCLINotInstalled, provider.ErrNeedsLogin, provider.ErrUnsupportedVersion}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Preflight(ctx, tc.p)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Preflight = %v, want %v", err, tc.want)
			}
			for _, other := range all {
				if !errors.Is(tc.want, other) && errors.Is(err, other) {
					t.Fatalf("the error for %s must not also match %v", tc.name, other)
				}
			}
			if d, _ := c.Detect(ctx, tc.p); d.Detail == "" {
				t.Fatal("the detection must carry a message the UI can show")
			}
		})
	}

	// Modes are refused clearly.
	noNotice := ok
	noNotice.AcceptedNotices = nil
	if _, err := c.Start(ctx, provider.SessionRequest{Profile: noNotice}); !errors.Is(err, provider.ErrNoticeRequired) || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("structured mode without the accepted notice = %v, want ErrNoticeRequired carrying the notice", err)
	}
	if _, err := c.Start(ctx, provider.SessionRequest{Profile: ok, Mode: provider.ModeTerminal}); !errors.Is(err, provider.ErrUnsupportedMode) {
		t.Fatalf("terminal mode is not available yet: %v", err)
	}
	if _, err := c.Start(ctx, provider.SessionRequest{Profile: provider.Profile{Kind: provider.KindClaude}}); !errors.Is(err, provider.ErrUnsupportedMode) {
		t.Fatalf("a profile with no mode must be refused: %v", err)
	}
}

func TestClaudeNeverReadsCredentialFiles(t *testing.T) {
	l := layout(t)
	p := claudeProfile(t, l, providertest.Hello)
	canary := filepath.Join(p.ConfigDir, ".credentials.json")
	if err := os.WriteFile(canary, []byte(`{"claudeAiOauth":{"accessToken":"CANARY-DO-NOT-READ"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(canary, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(canary, 0o600) })
	}

	events := runTurn(t, startClaude(t, p), "ping")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("a turn must work without the adapter touching credentials: %v", err)
	}
	for _, e := range events {
		if strings.Contains(e.Text, "CANARY") {
			t.Fatalf("credential content reached an event: %+v", e)
		}
	}
	if _, err := (provider.Claude{}).Detect(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	assertProviderNeverOpensFiles(t) // includes claude.go
}

// The official binary must be started as installed, with nothing that changes
// how it presents itself to Anthropic (terms: docs/research/provider-terms.md).
func TestClaudeLaunchesUnmodifiedBinary(t *testing.T) {
	p := claudeProfile(t, layout(t), providertest.Argv)
	p.Model = "some-model"
	s, err := provider.Claude{}.Start(context.Background(), provider.SessionRequest{
		Profile: p, Dir: t.TempDir(), SystemPrompt: "Be brief", PermissionMode: "acceptEdits", AllowedTools: []string{"Read", "Grep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const prompt = "the prompt text must not appear in the argument list"
	events := runTurn(t, s, prompt)

	var bin string
	var args, env []string
	var stdin string
	for _, e := range events {
		switch {
		case strings.HasPrefix(e.Text, "BIN:"):
			bin = strings.TrimPrefix(e.Text, "BIN:")
		case strings.HasPrefix(e.Text, "ARG:"):
			args = append(args, strings.TrimPrefix(e.Text, "ARG:"))
		case strings.HasPrefix(e.Text, "ENV:"):
			env = append(env, strings.TrimPrefix(e.Text, "ENV:"))
		case strings.HasPrefix(e.Text, "STDIN:"):
			stdin = strings.TrimPrefix(e.Text, "STDIN:")
		}
	}
	if bin != p.Binary {
		t.Fatalf("started %q, want exactly the profile's binary %q (no wrapper, no copy)", bin, p.Binary)
	}
	if len(args) == 0 || args[0] != "-p" {
		t.Fatalf("args = %v, want the documented print mode (-p) first", args)
	}
	for _, want := range []string{"--output-format", "stream-json", "--model", "some-model", "--append-system-prompt", "Be brief", "--permission-mode", "acceptEdits", "--allowedTools", "Read,Grep"} {
		if !slices.Contains(args, want) {
			t.Errorf("args %v lack %q", args, want)
		}
	}
	for _, a := range args {
		l := strings.ToLower(a)
		for _, bad := range []string{"http", "base-url", "user-agent", "header", "api-key", "token", prompt} {
			if strings.Contains(l, strings.ToLower(bad)) && a != "Be brief" {
				t.Errorf("argument %q could change how the CLI presents itself or leak the prompt", a)
			}
		}
	}
	if stdin != prompt {
		t.Errorf("stdin = %q, want the prompt (delivered on stdin, not on the command line)", stdin)
	}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_") {
			t.Errorf("environment variable %s would change the CLI's identity or billing", name)
		}
	}
}
