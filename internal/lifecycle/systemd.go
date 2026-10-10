package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Running the daemon on a server: a systemd unit. Servers are Linux with
// systemd; other init systems and other operating systems are out of scope
// (docs/VPS.md says so).

// UnitScope says whose service manager runs the daemon.
type UnitScope string

const (
	// ScopeUser is a user unit: the daemon runs as the person who installs it,
	// next to the official CLIs they installed and signed in. It needs no root.
	// To start at boot with nobody logged in, the user's "linger" must be on.
	ScopeUser UnitScope = "user"
	// ScopeSystem is a system unit run as a named user. It needs root to install.
	ScopeSystem UnitScope = "system"
)

// UnitName is the file name of the unit.
const UnitName = "aotusd.service"

// UnitOptions is what the unit needs. Everything in it ends up in a file that
// anyone on the machine may read, so there is no field for a secret.
type UnitOptions struct {
	Scope UnitScope
	// ExecPath is the absolute path of the daemon binary.
	ExecPath string
	// DataDir is the absolute data directory.
	DataDir string
	// User and Group run the daemon (system scope only).
	User, Group string
	// Tailnet asks for the tailnet listener; Port, Owner and TailscaleSocket
	// are its optional settings.
	Tailnet         bool
	Port            string
	Owner           string
	TailscaleSocket string
	// MemoryMax is a systemd memory limit for the daemon and everything it
	// starts, for example "3G". Empty means none.
	MemoryMax string
}

// ErrBadUnitOption means a value could not be written into a unit safely.
var ErrBadUnitOption = errors.New("lifecycle: unsuitable value for a systemd unit")

// validUnitValue refuses what could change the structure of the unit: line
// breaks and other control characters, and anything that is not printable.
func validUnitValue(name, v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return fmt.Errorf("%w: %s contains a control character", ErrBadUnitOption, name)
		}
	}
	return nil
}

// systemdQuote makes one word of an ExecStart line: plain when it is safe,
// double-quoted with systemd's escapes when not. `%` is doubled because
// systemd expands specifiers in unit files.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

// Unit renders the unit file.
func Unit(o UnitOptions) (string, error) {
	if o.Scope == "" {
		o.Scope = ScopeUser
	}
	if o.Scope != ScopeUser && o.Scope != ScopeSystem {
		return "", fmt.Errorf("%w: scope %q", ErrBadUnitOption, o.Scope)
	}
	// Unit files are for Linux: their paths are POSIX paths whatever machine writes them.
	if !strings.HasPrefix(o.ExecPath, "/") || !strings.HasPrefix(o.DataDir, "/") {
		return "", fmt.Errorf("%w: the daemon and the data directory need absolute paths", ErrBadUnitOption)
	}
	for name, v := range map[string]string{"daemon path": o.ExecPath, "data directory": o.DataDir, "user": o.User, "group": o.Group,
		"port": o.Port, "owner": o.Owner, "tailscale socket": o.TailscaleSocket, "memory limit": o.MemoryMax} {
		if err := validUnitValue(name, v); err != nil {
			return "", err
		}
	}
	if o.Scope == ScopeSystem && o.User == "" {
		return "", fmt.Errorf("%w: a system unit needs the user to run as", ErrBadUnitOption)
	}
	if o.Port != "" {
		if n, err := strconv.Atoi(o.Port); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%w: port %q", ErrBadUnitOption, o.Port)
		}
	}
	if o.MemoryMax != "" && strings.Trim(o.MemoryMax, "0123456789KMGTkmgt.%") != "" {
		return "", fmt.Errorf("%w: memory limit %q", ErrBadUnitOption, o.MemoryMax)
	}

	args := []string{systemdQuote(o.ExecPath), "--data-dir", systemdQuote(o.DataDir)}
	if o.Tailnet {
		args = append(args, "--tailnet")
		if o.Port != "" {
			args = append(args, "--tailnet-port", o.Port)
		}
		if o.Owner != "" {
			args = append(args, "--owner", systemdQuote(o.Owner))
		}
		if o.TailscaleSocket != "" {
			args = append(args, "--tailscale-socket", systemdQuote(o.TailscaleSocket))
		}
	}

	var b strings.Builder
	b.WriteString("# Written by `aotusd --service install`. Safe to delete with `aotusd --service uninstall`.\n")
	b.WriteString("# It holds no secret: the daemon learns who calls from Tailscale, and API keys live in the\n")
	b.WriteString("# operating system's credential store.\n\n")
	b.WriteString("[Unit]\nDescription=Aotus daemon (runs your AI employees)\nDocumentation=https://github.com/ownerng/aotus/blob/main/docs/VPS.md\n")
	b.WriteString("After=network-online.target tailscaled.service\nWants=network-online.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	if o.Scope == ScopeSystem {
		fmt.Fprintf(&b, "User=%s\n", o.User)
		if o.Group != "" {
			fmt.Fprintf(&b, "Group=%s\n", o.Group)
		}
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(args, " "))
	// The CLIs the employees run are usually installed in the user's own
	// directories, which a service's minimal PATH does not include.
	if o.Scope == ScopeUser {
		b.WriteString("Environment=PATH=%h/.local/bin:%h/.npm-global/bin:%h/.bun/bin:%h/go/bin:/usr/local/bin:/usr/bin:/bin\n")
	} else {
		b.WriteString("Environment=PATH=/usr/local/bin:/usr/bin:/bin\n")
	}
	b.WriteString("Restart=on-failure\nRestartSec=5\n")
	// The daemon stops its employees cleanly (terminal sessions come back on the
	// next start); give it time, then systemd ends whatever is left in the
	// service's control group, so nothing is orphaned.
	b.WriteString("TimeoutStopSec=30\nKillMode=control-group\n")
	b.WriteString("TasksMax=4096\nLimitNOFILE=65536\n")
	if o.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", o.MemoryMax)
	}
	// Employees must not be able to gain privileges (sudo, setuid programs).
	b.WriteString("NoNewPrivileges=yes\n")
	if o.Scope == ScopeSystem {
		b.WriteString("ProtectSystem=full\nProtectKernelTunables=yes\nProtectControlGroups=yes\nRestrictSUIDSGID=yes\n")
	}
	b.WriteString("\n[Install]\n")
	if o.Scope == ScopeSystem {
		b.WriteString("WantedBy=multi-user.target\n")
	} else {
		b.WriteString("WantedBy=default.target\n")
	}
	return b.String(), nil
}

// UnitDir is where unit files of a scope go: the user's own config directory,
// or /etc/systemd/system.
func UnitDir(scope UnitScope) (string, error) {
	if scope == ScopeSystem {
		return "/etc/systemd/system", nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// WriteUnit writes the unit file to dir (readable by everyone, writable by
// the owner) and returns its path.
func WriteUnit(dir, text string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // unit directories are world-readable by design
		return "", err
	}
	path := filepath.Join(dir, UnitName)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // a unit file holds no secret
		return "", err
	}
	return path, nil
}

// RemoveUnit deletes the unit file; a missing one is not an error.
func RemoveUnit(dir string) (string, error) {
	path := filepath.Join(dir, UnitName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return path, err
	}
	return path, nil
}
