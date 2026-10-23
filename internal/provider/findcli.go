package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"aotus/internal/proc"
)

// cli names the official CLI of a kind of profile.
type cli struct {
	program string // the executable's name, without extension
	title   string // how the user knows it
}

var clis = map[Kind]cli{
	KindClaude: {program: "claude", title: "Claude Code"},
	KindCodex:  {program: "codex", title: "Codex CLI"},
}

// installDirs are the folders, relative to the home directory, where the
// official installers and package managers put the CLIs. A daemon started at
// login or by systemd often has a shorter PATH than the user's shell, so these
// are tried after PATH.
var installDirs = []string{
	".local/bin",      // Claude Code's native installer (also on Windows)
	".claude/local",   // Claude Code's older local install
	".npm-global/bin", // npm with a user prefix
	".bun/bin",
}

// windowsDirs are the Windows-only install folders, relative to the home.
var windowsDirs = []string{"AppData/Roaming/npm"}

// systemDirs are tried last on macOS and Linux, for package managers whose
// folder is not on a short PATH (Homebrew on Apple Silicon).
var systemDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// HasCLI reports whether profiles of this kind run an official CLI (an API
// profile does not).
func HasCLI(k Kind) bool {
	_, ok := clis[k]
	return ok
}

// FindCLI returns the absolute path of the official CLI of a kind: first on
// the daemon's PATH, then in the folders the official installers use (ADR
// 0008: found on PATH or chosen by the user). A miss wraps ErrCLINotInstalled
// with what to do about it.
func FindCLI(k Kind) (string, error) {
	c, ok := clis[k]
	if !ok {
		return "", fmt.Errorf("provider: %s profiles have no CLI", k)
	}
	if p, err := proc.LookPath(c.program); err == nil {
		return p, nil
	} else if !errors.Is(err, proc.ErrBinaryNotFound) {
		return "", err
	}
	homeDirs, abs := installDirs, systemDirs
	if runtime.GOOS == "windows" {
		homeDirs, abs = append(slices.Clone(installDirs), windowsDirs...), nil
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range homeDirs {
			dirs = append(dirs, filepath.Join(home, filepath.FromSlash(d)))
		}
	}
	dirs = append(dirs, abs...)
	for _, d := range dirs {
		if p, err := proc.LookPath(filepath.Join(d, c.program)); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s (%s) was not found on the daemon's PATH or in %s; install it, or give the path of its binary",
		ErrCLINotInstalled, c.title, c.program, strings.Join(dirs, ", "))
}
