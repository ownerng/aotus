//go:build realcli

package provider_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/provider"
)

// Integration test against the real, installed Claude Code. Run it with
//
//	go test -tags realcli -run TestRealClaude ./internal/provider
//
// It uses a brand-new, logged-out profile, so it never uses the owner's login
// and consumes no subscription quota.
func TestRealClaudeLoggedOutProfile(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	l := layout(t)
	p, err := provider.NewProfile(l, provider.KindClaude, "real logged out", bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureConfigDir(); err != nil {
		t.Fatal(err)
	}
	p.Mode = provider.ModeStructured
	p.AcceptedNotices = []string{provider.NoticeClaudeHeadless}

	c := provider.Claude{}
	d, err := c.Detect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detection: %+v", d)
	if !d.Installed || !d.VersionOK || d.Login != provider.LoginLoggedOut {
		t.Fatalf("want an installed, supported, logged-out CLI; got %+v", d)
	}

	s, err := c.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	events := runTurn(t, s, "Reply with one word")
	for _, e := range events {
		t.Logf("event: %+v", e)
	}
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("the real CLI's failure must still follow the event contract: %v", err)
	}
	var code provider.ErrorCode
	for _, e := range events {
		if e.Kind == provider.EventError {
			code = e.Code
		}
	}
	if code != provider.CodeNeedsLogin {
		t.Fatalf("error code = %q, want needs_login for a CLI that is not logged in", code)
	}
}

// The same check for the real, installed Codex CLI, with a fresh logged-out
// profile.
func TestRealCodexLoggedOutProfile(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not installed")
	}
	// Codex refuses to set up its home under a temporary directory, so use a
	// directory next to the test sources.
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	_ = l.Ensure()
	p, err := provider.NewProfile(l, provider.KindCodex, "real logged out", bin)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	p.ConfigDir = filepath.Join(home, ".cache", "aotus-realcli-test", p.ID)
	if err := p.EnsureConfigDir(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(p.ConfigDir)) })
	p.Mode = provider.ModeStructured

	c := provider.Codex{}
	d, err := c.Detect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detection: %+v", d)
	if !d.Installed || !d.VersionOK || d.Login != provider.LoginLoggedOut {
		t.Fatalf("want an installed, supported, logged-out CLI; got %+v", d)
	}
	s, err := c.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	events := runTurn(t, s, "Reply with one word")
	for _, e := range events {
		t.Logf("event: %+v", e)
	}
	if err := provider.ValidateTurn(events); err != nil {
		t.Fatalf("the real CLI's failure must still follow the event contract: %v", err)
	}
}

// Hosts the real Claude Code interface in a pseudo-terminal, with a fresh
// logged-out profile: nothing is sent to the model.
func TestRealClaudeTerminalMode(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	l := layout(t)
	p, err := provider.NewProfile(l, provider.KindClaude, "real terminal", bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureConfigDir(); err != nil {
		t.Fatal(err)
	}
	s, err := provider.Claude{}.Start(context.Background(), provider.SessionRequest{Profile: p, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ts, ok := s.(provider.TerminalSession)
	if !ok {
		t.Fatalf("%T is not a terminal session", s)
	}
	t.Cleanup(func() { _ = ts.Close() })
	if err := ts.Launch(context.Background(), 40, 120); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	replay, _, cancel := ts.Terminal().Subscribe()
	cancel()
	text := strings.Join(strings.Fields(regexp.MustCompile(`\x1b\[[0-9;?<>=]*[a-zA-Z]|\x1b[()][A-Z0-9]|\x1b\][^\x07]*\x07`).ReplaceAllString(string(replay), " ")), " ")
	if len(text) > 400 {
		text = text[:400]
	}
	t.Logf("%d bytes of terminal output; visible text: %s", len(replay), text)
	if len(replay) < 200 {
		t.Fatal("the real interface drew almost nothing")
	}
	if err := ts.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	for ev := range ts.Events() {
		if ev.Kind == provider.EventDone {
			if ev.Done.Reason != provider.DoneCanceled {
				t.Fatalf("done = %+v", ev.Done)
			}
			return
		}
	}
}
