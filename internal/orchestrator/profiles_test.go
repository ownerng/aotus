package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"aotus/internal/provider"
)

func TestCreateProfileFindsTheCLI(t *testing.T) {
	var asked []provider.Kind
	r := newRig(t, Options{FindCLI: func(k provider.Kind) (string, error) {
		asked = append(asked, k)
		return "/opt/found/" + string(k), nil
	}})
	ctx := context.Background()

	p, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindCodex, Name: "work"})
	if err != nil {
		t.Fatalf("CreateProfile without a path: %v", err)
	}
	if p.Binary != "/opt/found/codex" {
		t.Fatalf("Binary = %q; want the path FindCLI returned", p.Binary)
	}
	stored, err := r.svc.Profile(ctx, p.ID)
	if err != nil || stored.Binary != "/opt/found/codex" {
		t.Fatalf("stored profile = %+v, %v; want the found path stored", stored, err)
	}

	// A path given by the user is kept as it is.
	p, err = r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "mine", Binary: "/home/me/bin/claude"})
	if err != nil || p.Binary != "/home/me/bin/claude" {
		t.Fatalf("CreateProfile with a path = %q, %v; want the given path", p.Binary, err)
	}
	if len(asked) != 1 || asked[0] != provider.KindCodex {
		t.Fatalf("FindCLI was asked for %v; want only codex (the profile without a path)", asked)
	}
}

func TestCreateProfileWithoutTheCLIExplainsWhy(t *testing.T) {
	missing := fmt.Errorf("%w: Codex CLI (codex) was not found on the daemon's PATH; install it, or give the path of its binary", provider.ErrCLINotInstalled)
	r := newRig(t, Options{FindCLI: func(provider.Kind) (string, error) { return "", missing }})
	ctx := context.Background()

	_, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindCodex, Name: "work"})
	if !errors.Is(err, ErrCLINotInstalled) || !strings.Contains(err.Error(), "install it") {
		t.Fatalf("CreateProfile with no CLI: %v; want ErrCLINotInstalled saying what to do", err)
	}
	if ps, _ := r.svc.Profiles(ctx); len(ps) != 1 { // the rig's own profile only
		t.Fatalf("a profile was stored although its CLI is missing: %+v", ps)
	}

	_, err = r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "!!!", Binary: "/usr/bin/claude"})
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("CreateProfile with an unusable name: %v; want ErrInvalidProfile", err)
	}
}

func TestCreateProfileStoresTheDefaultMode(t *testing.T) {
	r := newRig(t, Options{FindCLI: func(k provider.Kind) (string, error) { return "/opt/found/" + string(k), nil }})
	ctx := context.Background()
	for _, tc := range []struct {
		kind provider.Kind
		mode provider.Mode
		want provider.Mode
	}{
		{provider.KindCodex, "", provider.ModeStructured},
		{provider.KindClaude, "", provider.ModeStructured}, // ADR 0016: chat by default, behind its notice
		{provider.KindClaude, provider.ModeTerminal, provider.ModeTerminal},
		{provider.KindCodex, provider.ModeTerminal, provider.ModeTerminal},
	} {
		p, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: tc.kind, Name: string(tc.kind) + "-" + string(tc.mode), Mode: tc.mode})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := r.svc.Profile(ctx, p.ID)
		if err != nil || stored.Mode != tc.want {
			t.Errorf("%s profile created with mode %q is stored as %q, %v; want %q", tc.kind, tc.mode, stored.Mode, err, tc.want)
		}
	}
}

func TestOnlyOneComputerLoginPerCLI(t *testing.T) {
	r := newRig(t, Options{FindCLI: func(k provider.Kind) (string, error) { return "/opt/found/" + string(k), nil }})
	ctx := context.Background()

	mine, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "My Claude", ComputerLogin: true})
	if err != nil {
		t.Fatalf("Claude on the computer's sign-in: %v", err)
	}
	if stored, _ := r.svc.Profile(ctx, mine.ID); !stored.ComputerLogin {
		t.Fatal("the computer's sign-in was not stored")
	}
	_, err = r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "Again", ComputerLogin: true})
	if !errors.Is(err, ErrInvalidProfile) || !strings.Contains(err.Error(), "My Claude") {
		t.Fatalf("a second Claude profile on the computer's sign-in: %v; want ErrInvalidProfile naming the first", err)
	}
	if _, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindCodex, Name: "My Codex", ComputerLogin: true}); err != nil {
		t.Fatalf("Codex on the computer's sign-in next to Claude: %v", err)
	}
	if _, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "Work", ComputerLogin: false}); err != nil {
		t.Fatalf("a separate Claude sign-in next to the computer's: %v", err)
	}
	// Accepting a notice rewrites the profile: the setting must survive it.
	if err := r.mgr.AcceptNotice(ctx, mine.ID, provider.NoticeClaudeHeadless); err != nil {
		t.Fatal(err)
	}
	if stored, _ := r.svc.Profile(ctx, mine.ID); !stored.ComputerLogin {
		t.Fatal("accepting a notice dropped the computer's sign-in")
	}
}

func TestModeIsKnownBeforeTheSessionStarts(t *testing.T) {
	r := newRig(t, Options{FindCLI: func(k provider.Kind) (string, error) { return "/opt/found/" + string(k), nil }})
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		mode provider.Mode
		want provider.Mode
	}{
		{"chat", "", provider.ModeStructured},
		{"term", provider.ModeTerminal, provider.ModeTerminal},
	} {
		p, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "p-" + tc.name, Mode: tc.mode})
		if err != nil {
			t.Fatal(err)
		}
		emp := r.employee(t, tc.name, p)
		if st := r.mgr.Status(emp.ID); st.Mode != "" {
			t.Fatalf("%s: a session exists already (%s); the test needs one that has not started", tc.name, st.Mode)
		}
		if got := r.mgr.ModeOf(ctx, emp); got != tc.want {
			t.Errorf("%s: ModeOf before the session starts = %q, want %q (a client must know whether to offer a chat or a terminal)", tc.name, got, tc.want)
		}
	}
}

func TestFindCLIsCreatesNothing(t *testing.T) {
	var probed []provider.Profile
	r := newRig(t, Options{
		FindCLI: func(k provider.Kind) (string, error) {
			if k == provider.KindCodex {
				return "", fmt.Errorf("%w: Codex CLI (codex) was not found", provider.ErrCLINotInstalled)
			}
			return "/opt/found/" + string(k), nil
		},
		Probe: func(_ context.Context, p provider.Profile) (provider.Detection, error) {
			probed = append(probed, p)
			return provider.Detection{Installed: true, Version: "2.1.310", VersionOK: true, Login: provider.LoginLoggedIn}, nil
		},
	})
	ctx := context.Background()
	before, _ := r.svc.Profiles(ctx)
	entries, _ := os.ReadDir(r.layout.ProfilesDir())

	found, err := r.mgr.FindCLIs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Kind != provider.KindClaude || found[1].Kind != provider.KindCodex {
		t.Fatalf("found = %+v; want Claude Code then Codex", found)
	}
	c, x := found[0], found[1]
	if c.Path != "/opt/found/claude" || c.Login != provider.LoginLoggedIn || c.Version != "2.1.310" || c.Title != "Claude Code" {
		t.Errorf("claude = %+v", c)
	}
	if x.Path != "" || !strings.Contains(x.Detail, "not found") {
		t.Errorf("codex = %+v; want not installed, saying so", x)
	}
	// It asked the CLI as the person's terminal would: its own folder.
	if len(probed) != 1 || !probed[0].ComputerLogin || probed[0].Binary != "/opt/found/claude" {
		t.Errorf("probed %+v; want one probe of claude on the computer's sign-in", probed)
	}
	// And it created nothing.
	after, _ := r.svc.Profiles(ctx)
	entriesAfter, _ := os.ReadDir(r.layout.ProfilesDir())
	if len(after) != len(before) || len(entriesAfter) != len(entries) {
		t.Fatalf("FindCLIs created something: %d -> %d profiles, %d -> %d folders", len(before), len(after), len(entries), len(entriesAfter))
	}

	// Once linked, it says by which profile.
	if _, err := r.mgr.CreateProfile(ctx, NewProfile{Kind: provider.KindClaude, Name: "Mine", ComputerLogin: true}); err != nil {
		t.Fatal(err)
	}
	found, _ = r.mgr.FindCLIs(ctx)
	if found[0].Linked != "Mine" {
		t.Fatalf("after linking, claude.Linked = %q; want Mine", found[0].Linked)
	}
}
