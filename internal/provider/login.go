package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aotus/internal/proc"
)

// Login is what the CLI says about a profile's login. It carries only
// non-sensitive facts: the CLIs also print an e-mail address and an
// organization, which are discarded.
type Login struct {
	State  LoginState
	Method string // for example "claude.ai" or "ChatGPT"
	Plan   string // for example "pro"
}

// loginTimeout bounds the status command.
const loginTimeout = 20 * time.Second

// CheckLogin asks the profile's own CLI whether it is logged in, by running
// its status command with the profile's environment. It never opens any file
// of the CLI's configuration directory: only the CLI itself looks at its
// credentials.
//
// A CLI can report "logged in" with a token that has expired (seen with Codex
// CLI); turns that fail with an authentication error must be treated as
// needing a new login regardless of this result.
func CheckLogin(ctx context.Context, p Profile, lookup func(string) (string, bool)) (Login, error) {
	var args []string
	switch p.Kind {
	case KindClaude:
		args = []string{"auth", "status"}
	case KindCodex:
		args = []string{"login", "status"}
	default:
		return Login{State: LoginUnknown}, fmt.Errorf("provider: %s has no login status command", p.Kind)
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	run, err := proc.Start(ctx, proc.Spec{Path: p.Binary, Args: args, Env: ProfileEnv(p, lookup)})
	if err != nil {
		return Login{State: LoginUnknown}, err
	}
	_ = run.Stdin().Close()
	// Claude Code prints JSON on stdout. Codex CLI prints its one-line status
	// on stderr (and exits 1 when logged out), so both streams are read.
	var stdout, all []string
	for l := range run.Lines() {
		all = append(all, l.Text)
		if l.Stream == proc.Stdout {
			stdout = append(stdout, l.Text)
		}
	}
	exit := run.Wait()
	if exit.Canceled {
		return Login{State: LoginUnknown}, errors.New("provider: the login status command timed out")
	}
	if p.Kind == KindCodex {
		return parseLogin(p.Kind, all)
	}
	return parseLogin(p.Kind, stdout)
}

// parseLogin reads the lines the status command printed: stdout for Claude
// Code, stdout and stderr together for Codex CLI.
func parseLogin(kind Kind, stdout []string) (Login, error) {
	switch kind {
	case KindClaude:
		var v struct {
			LoggedIn         bool   `json:"loggedIn"`
			AuthMethod       string `json:"authMethod"`
			SubscriptionType string `json:"subscriptionType"`
		}
		if err := json.Unmarshal([]byte(strings.Join(stdout, "\n")), &v); err != nil {
			return Login{State: LoginUnknown}, fmt.Errorf("provider: unreadable claude auth status: %w", err)
		}
		if !v.LoggedIn {
			return Login{State: LoginLoggedOut}, nil
		}
		return Login{State: LoginLoggedIn, Method: v.AuthMethod, Plan: v.SubscriptionType}, nil
	case KindCodex:
		// The status line may be preceded by warnings: take the last line that
		// is a status.
		for i := len(stdout) - 1; i >= 0; i-- {
			line := strings.TrimSpace(stdout[i])
			switch {
			case strings.HasPrefix(line, "Not logged in"):
				return Login{State: LoginLoggedOut}, nil
			case strings.HasPrefix(line, "Logged in"):
				_, method, _ := strings.Cut(line, "using ")
				return Login{State: LoginLoggedIn, Method: method}, nil
			}
		}
		return Login{State: LoginUnknown}, fmt.Errorf("provider: unrecognized codex login status %q", strings.Join(stdout, " | "))
	}
	return Login{State: LoginUnknown}, fmt.Errorf("provider: no parser for %s", kind)
}
