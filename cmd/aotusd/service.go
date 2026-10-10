package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"aotus/internal/lifecycle"
	"aotus/internal/proc"
)

// serviceRequest is `aotusd --service ...` with the settings it should install.
type serviceRequest struct {
	Action          string
	Scope           lifecycle.UnitScope
	User, Dir       string
	DataDir         string
	Tailnet         bool
	Port, Owner     string
	TailscaleSocket string
	MaxTurns        int
}

// tsSocketIfSet passes the Tailscale socket into the unit only when the user
// chose one; the default is the daemon's own.
func tsSocketIfSet(fs *flag.FlagSet, value string) string {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "tailscale-socket" })
	if set {
		return value
	}
	return ""
}

// runCommand runs a program and returns its combined output. It goes through
// proc, the one place that starts processes. The environment is the caller's:
// systemctl --user needs XDG_RUNTIME_DIR and the session bus address.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := proc.Start(ctx, proc.Spec{Path: name, Args: args, Env: os.Environ()})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for l := range p.Lines() {
		out.WriteString(l.Text + "\n")
	}
	exit := p.Wait()
	if exit.Code != 0 {
		return out.String(), fmt.Errorf("%s %s: exit code %d: %s", name, strings.Join(args, " "), exit.Code, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// manageService installs, removes or reports the systemd unit that runs the
// daemon. It never starts the daemon itself.
func manageService(ctx context.Context, opts options, r serviceRequest, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "aotusd: "+format+"\n", a...)
		return 1
	}
	if opts.goos != "linux" {
		return fail("--service is only for Linux servers with systemd (this is %s); on a desktop, `aotusd --autostart on` starts the daemon at login", opts.goos)
	}
	switch r.Action {
	case "install", "uninstall", "status":
	default:
		fmt.Fprintf(stderr, "aotusd: --service takes install, uninstall or status, not %q\n", r.Action)
		return 2
	}
	if r.Scope == "" {
		r.Scope = lifecycle.ScopeUser
	}
	if r.Scope != lifecycle.ScopeUser && r.Scope != lifecycle.ScopeSystem {
		fmt.Fprintf(stderr, "aotusd: --service-scope takes user or system, not %q\n", r.Scope)
		return 2
	}
	dir := r.Dir
	if dir == "" {
		var err error
		if dir, err = lifecycle.UnitDir(r.Scope); err != nil {
			return fail("%v", err)
		}
	}
	// With --service-dir the files are only written (packaging, tests); the
	// service manager is not asked to do anything.
	manage := r.Dir == ""
	systemctl := func(args ...string) error {
		if !manage {
			return nil
		}
		full := args
		if r.Scope == lifecycle.ScopeUser {
			full = append([]string{"--user"}, args...)
		}
		_, err := opts.runCmd(ctx, "systemctl", full...)
		return err
	}
	unitPath := filepath.Join(dir, lifecycle.UnitName)

	switch r.Action {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return fail("cannot find its own path: %v", err)
		}
		if exe, err = filepath.Abs(exe); err != nil {
			return fail("%v", err)
		}
		dataDir, err := filepath.Abs(r.DataDir)
		if err != nil {
			return fail("%v", err)
		}
		runAs := r.User
		if r.Scope == lifecycle.ScopeUser {
			runAs = ""
		} else if runAs == "" {
			return fail("a system service needs --service-user NAME: the person whose official CLIs the employees run")
		}
		text, err := lifecycle.Unit(lifecycle.UnitOptions{
			Scope: r.Scope, ExecPath: exe, DataDir: dataDir, User: runAs, Tailnet: r.Tailnet, Port: r.Port, Owner: r.Owner, TailscaleSocket: r.TailscaleSocket, MaxTurns: r.MaxTurns,
		})
		if err != nil {
			return fail("%v", err)
		}
		if _, err := lifecycle.WriteUnit(dir, text); err != nil {
			return fail("writing %s: %v", unitPath, err)
		}
		fmt.Fprintf(stdout, "wrote %s\n", unitPath)
		if err := systemctl("daemon-reload"); err != nil {
			return fail("%v", err)
		}
		if err := systemctl("enable", "--now", lifecycle.UnitName); err != nil {
			return fail("the unit was written but could not be started: %v", err)
		}
		if manage {
			fmt.Fprintln(stdout, "the daemon is enabled and started")
		}
		if manage && r.Scope == lifecycle.ScopeUser {
			// Without linger a user service stops when the last session ends and
			// does not start at boot. Turning it on may need an administrator.
			if cu, err := opts.currentUser(); err == nil {
				if _, err := opts.runCmd(ctx, "loginctl", "enable-linger", cu); err != nil {
					fmt.Fprintf(stderr, "aotusd: warning: could not turn on linger, so the daemon will only run while you are logged in. Run: sudo loginctl enable-linger %s\n", cu)
				} else {
					fmt.Fprintf(stdout, "linger is on for %s: the daemon starts at boot, with nobody logged in\n", cu)
				}
			}
		}
		return 0
	case "uninstall":
		// Stopping can fail when it was never installed; removal still goes on.
		if err := systemctl("disable", "--now", lifecycle.UnitName); err != nil {
			fmt.Fprintf(stderr, "aotusd: note: %v\n", err)
		}
		if _, err := lifecycle.RemoveUnit(dir); err != nil {
			return fail("removing %s: %v", unitPath, err)
		}
		if err := systemctl("daemon-reload"); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "removed %s (your data directory is untouched)\n", unitPath)
		return 0
	default: // status
		_, statErr := os.Stat(unitPath)
		if errors.Is(statErr, os.ErrNotExist) {
			fmt.Fprintf(stdout, "service: not installed (%s does not exist)\n", unitPath)
			return 0
		}
		fmt.Fprintf(stdout, "service: installed at %s\n", unitPath)
		if manage {
			args := []string{"is-active", lifecycle.UnitName}
			if r.Scope == lifecycle.ScopeUser {
				args = append([]string{"--user"}, args...)
			}
			out, _ := opts.runCmd(ctx, "systemctl", args...)
			fmt.Fprintf(stdout, "state: %s\n", strings.TrimSpace(out))
		}
		return 0
	}
}

func currentUserName() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
