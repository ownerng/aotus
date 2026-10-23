package provider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeProgram writes an executable named program into dir and returns its path.
func fakeProgram(t *testing.T, dir, program string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, program)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // the test needs an executable
		t.Fatal(err)
	}
	return p
}

// isolate gives FindCLI an empty PATH, an empty home and no system folders.
func isolate(t *testing.T) (pathDir, home string) {
	t.Helper()
	pathDir, home = t.TempDir(), t.TempDir()
	t.Setenv("PATH", pathDir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	old := systemDirs
	systemDirs = nil
	t.Cleanup(func() { systemDirs = old })
	return pathDir, home
}

func TestFindCLIUsesPathThenInstallFolders(t *testing.T) {
	pathDir, home := isolate(t)

	inHome := fakeProgram(t, filepath.Join(home, ".local", "bin"), "claude")
	got, err := FindCLI(KindClaude)
	if err != nil || !sameFile(t, got, inHome) {
		t.Fatalf("FindCLI(claude) with the CLI only in ~/.local/bin = %q, %v; want %q", got, err, inHome)
	}

	onPath := fakeProgram(t, pathDir, "claude")
	got, err = FindCLI(KindClaude)
	if err != nil || !sameFile(t, got, onPath) {
		t.Fatalf("FindCLI(claude) with the CLI on PATH = %q, %v; want %q (PATH comes first)", got, err, onPath)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("FindCLI returned %q; want an absolute path", got)
	}

	codex := fakeProgram(t, filepath.Join(home, ".npm-global", "bin"), "codex")
	if got, err := FindCLI(KindCodex); err != nil || !sameFile(t, got, codex) {
		t.Fatalf("FindCLI(codex) = %q, %v; want %q", got, err, codex)
	}
}

func TestFindCLIReportsWhatIsMissing(t *testing.T) {
	_, home := isolate(t)
	_, err := FindCLI(KindCodex)
	if !errors.Is(err, ErrCLINotInstalled) {
		t.Fatalf("FindCLI(codex) with no CLI anywhere: %v; want ErrCLINotInstalled", err)
	}
	for _, want := range []string{"Codex CLI", "codex", "PATH", filepath.Join(home, ".local", "bin"), "give the path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, err := FindCLI(KindOpenAI); err == nil || errors.Is(err, ErrCLINotInstalled) {
		t.Fatalf("FindCLI(openai-api) = %v; want an error that is not ErrCLINotInstalled (API profiles have no CLI)", err)
	}
	if HasCLI(KindOpenAI) || !HasCLI(KindClaude) || !HasCLI(KindCodex) {
		t.Fatal("HasCLI: only Claude Code and Codex profiles run a CLI")
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(fa, fb)
}
