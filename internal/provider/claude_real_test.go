//go:build realcli

package provider_test

import (
	"context"
	"os/exec"
	"testing"

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
