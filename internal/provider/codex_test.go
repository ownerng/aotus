package provider_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aotus/internal/datadir"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
)

func codexProfile(t *testing.T, l datadir.Layout, sc providertest.Scenario) provider.Profile {
	t.Helper()
	p := fakeProfile(t, l, provider.KindCodex, "codex", "in")
	p.ExtraEnv[providertest.EnvFakeScenario] = string(sc)
	p.ExtraEnv[providertest.EnvFakePidFile] = filepath.Join(t.TempDir(), "pids")
	p.Mode = provider.ModeStructured
	return p
}

func startCodex(t *testing.T, p provider.Profile) provider.Session {
	t.Helper()
	s, err := provider.Codex{}.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCodexPassesContract(t *testing.T) {
	providertest.RunContract(t, providertest.Contract{
		Process: true,
		New: func(t *testing.T, sc providertest.Scenario) (provider.Session, providertest.Probe) {
			t.Helper()
			p := codexProfile(t, layout(t), sc)
			return startCodex(t, p), providertest.Probe{PidFile: p.ExtraEnv[providertest.EnvFakePidFile]}
		},
	})
}

func replayCodex(t *testing.T, file string) []provider.Event {
	t.Helper()
	p := codexProfile(t, layout(t), providertest.Replay)
	abs, err := filepath.Abs(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	p.ExtraEnv[providertest.EnvFakeReplay] = abs
	return runTurn(t, startCodex(t, p), "ping")
}

// The failed-turn recording is real output of Codex CLI 0.160.0.
func TestCodexParsesRecordedStream(t *testing.T) {
	events := replayCodex(t, "codex-failed-model.jsonl")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("the recorded turn violates the event contract: %v\n%v", err, events)
	}
	var errs []provider.Event
	var session bool
	for _, e := range events {
		switch e.Kind {
		case provider.EventError:
			errs = append(errs, e)
		case provider.EventSession:
			session = e.SessionID != ""
		}
	}
	if !session {
		t.Error("the thread ID must be reported as the session")
	}
	if len(errs) != 1 || errs[0].Code != provider.CodeModelUnsupported {
		t.Fatalf("errors = %+v, want exactly one model_unsupported error (the model-metadata warning is not a failure)", errs)
	}
	if strings.Contains(errs[0].Text, `{"type"`) || !strings.Contains(errs[0].Text, "not supported when using Codex with a ChatGPT account") {
		t.Fatalf("error text = %q, want the readable message, not the raw JSON", errs[0].Text)
	}
	if events[len(events)-1].Done.Reason != provider.DoneFailed {
		t.Fatalf("done = %+v, want failed", events[len(events)-1].Done)
	}

	// The success path is covered by a hand-written stream that follows
	// Codex's documentation, until a real recording exists (testdata/README.md).
	ok := replayCodex(t, "codex-success-synthetic.jsonl")
	if err := provider.ValidateTurn(ok); err != nil {
		t.Fatalf("%v\n%v", err, ok)
	}
	if got := eventsText(ok); got != "pong" {
		t.Fatalf("answer = %q, want pong", got)
	}
	var req, res bool
	for _, e := range ok {
		if e.Kind == provider.EventToolRequest && e.Tool.Name == "command" && strings.Contains(string(e.Tool.Input), "ls") {
			req = true
		}
		if e.Kind == provider.EventToolResult && e.Tool.Output == "a.txt\nb.txt\n" && !e.Tool.IsError {
			res = true
		}
	}
	if !req || !res {
		t.Fatalf("the command must appear as a tool request and a successful result: %v", ok)
	}
	if done := ok[len(ok)-1].Done; done.Reason != provider.DoneCompleted || done.InputTokens != 120 || done.OutputTokens != 4 {
		t.Fatalf("done = %+v", done)
	}
}

func TestCodexNeverReadsCredentialFiles(t *testing.T) {
	p := codexProfile(t, layout(t), providertest.Hello)
	canary := filepath.Join(p.ConfigDir, "auth.json")
	if err := os.WriteFile(canary, []byte(`{"tokens":{"access_token":"CANARY-DO-NOT-READ"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(canary, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(canary, 0o600) })
	}
	events := runTurn(t, startCodex(t, p), "ping")
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("a turn must work without the adapter touching credentials: %v", err)
	}
	for _, e := range events {
		if strings.Contains(e.Text, "CANARY") {
			t.Fatalf("credential content reached an event: %+v", e)
		}
	}
	if _, err := (provider.Codex{}).Detect(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	assertProviderNeverOpensFiles(t)
}

func TestCodexReportsMissingLoggedOutAndOldCLI(t *testing.T) {
	ctx, l, c := context.Background(), layout(t), provider.Codex{}
	ok := codexProfile(t, l, providertest.Hello)
	if d, err := c.Detect(ctx, ok); err != nil || !d.Installed || !d.VersionOK || d.Login != provider.LoginLoggedIn || d.Version != "0.160.0" {
		t.Fatalf("a healthy CLI: %+v, %v", d, err)
	}
	missing := ok
	missing.Binary = filepath.Join(t.TempDir(), "no-codex")
	out := codexProfile(t, l, providertest.Hello)
	_ = os.WriteFile(filepath.Join(out.ConfigDir, "fake-login"), []byte("out"), 0o600)
	old := codexProfile(t, l, providertest.Hello)
	_ = os.WriteFile(filepath.Join(old.ConfigDir, "fake-version"), []byte("codex-cli 0.100.0"), 0o600)

	for name, tc := range map[string]struct {
		p    provider.Profile
		want error
	}{"missing": {missing, provider.ErrCLINotInstalled}, "logged out": {out, provider.ErrNeedsLogin}, "old": {old, provider.ErrUnsupportedVersion}} {
		if err := c.Preflight(ctx, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: Preflight = %v, want %v", name, err, tc.want)
		}
	}
	if _, err := c.Start(ctx, provider.SessionRequest{Profile: ok, Mode: provider.ModeAPI}); !errors.Is(err, provider.ErrUnsupportedMode) {
		t.Errorf("a mode Codex CLI does not have must be refused: %v", err)
	}
}

// How Codex is started: prompt on stdin, the official binary, a read-only
// sandbox by default, and resume with the thread ID.
func TestCodexCommandLine(t *testing.T) {
	p := codexProfile(t, layout(t), providertest.Argv)
	s := startCodex(t, p)
	events := runTurn(t, s, "do not put this in argv")
	var args []string
	var stdin, bin string
	for _, e := range events {
		switch {
		case strings.HasPrefix(e.Text, "BIN:"):
			bin = strings.TrimPrefix(e.Text, "BIN:")
		case strings.HasPrefix(e.Text, "ARG:"):
			args = append(args, strings.TrimPrefix(e.Text, "ARG:"))
		case strings.HasPrefix(e.Text, "STDIN:"):
			stdin = strings.TrimPrefix(e.Text, "STDIN:")
		}
	}
	if bin != p.Binary || stdin != "do not put this in argv" {
		t.Fatalf("binary %q, stdin %q", bin, stdin)
	}
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" exec ", " --json ", " -s read-only ", " - "} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %v lack %q", args, strings.TrimSpace(want))
		}
	}
	if strings.Contains(joined, "do not put this") {
		t.Error("the prompt must not be on the command line")
	}
	if strings.Contains(joined, "dangerously") {
		t.Error("the unsafe bypass flags must never be used")
	}
}
