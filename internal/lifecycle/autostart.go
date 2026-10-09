package lifecycle

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Autostart registers the daemon to start when the user logs in. It is
// per-user, never system-wide, and needs no administrator rights.
type Autostart interface {
	// Enable makes the daemon start at login. Calling it again is harmless.
	Enable() error
	// Disable removes the registration. Calling it again is harmless.
	Disable() error
	// Enabled reports whether the daemon is registered.
	Enabled() (bool, error)
}

// NewAutostart returns the autostart mechanism of this operating system for
// the daemon binary at exe: an XDG autostart entry on Linux, a LaunchAgent on
// macOS, the user's Run key in the registry on Windows.
func NewAutostart(exe string) (Autostart, error) {
	if !filepath.IsAbs(exe) {
		return nil, fmt.Errorf("lifecycle: autostart needs the absolute path of the daemon, got %q", exe)
	}
	return newPlatformAutostart(exe)
}

// fileAutostart is an autostart entry that is a single file.
type fileAutostart struct {
	path    string
	content []byte
}

func (a fileAutostart) Enable() error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil { //nolint:gosec // per-user autostart folders are normally 0755
		return fmt.Errorf("lifecycle: creating %s: %w", filepath.Dir(a.path), err)
	}
	if err := os.WriteFile(a.path, a.content, 0o644); err != nil { //nolint:gosec // the entry holds no secret
		return fmt.Errorf("lifecycle: writing %s: %w", a.path, err)
	}
	return nil
}

func (a fileAutostart) Disable() error {
	if err := os.Remove(a.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lifecycle: removing %s: %w", a.path, err)
	}
	return nil
}

func (a fileAutostart) Enabled() (bool, error) {
	_, err := os.Stat(a.path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// desktopEntry is the XDG autostart file (used by KDE, GNOME, Xfce...). The
// Exec value follows the Desktop Entry specification: the path is quoted and
// the characters it reserves are escaped.
func desktopEntry(exe string) []byte {
	return []byte("[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Aotus\n" +
		"Comment=Keeps your Aotus employees working in the background\n" +
		"Exec=" + desktopExec(exe) + "\n" +
		"Terminal=false\n" +
		"NoDisplay=true\n" +
		"X-GNOME-Autostart-enabled=true\n")
}

func desktopExec(exe string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range exe {
		switch r {
		case '"', '`', '$', '\\':
			b.WriteByte('\\')
		}
		if r == '%' {
			b.WriteByte('%') // a literal percent sign is written %%
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// launchAgentLabel identifies the LaunchAgent.
const launchAgentLabel = "dev.aotus.daemon"

// launchAgentPlist is the macOS LaunchAgent: started at login and restarted if
// it crashes (the instance lock makes a second copy harmless).
func launchAgentPlist(exe string) []byte {
	var esc bytes.Buffer
	_ = xml.EscapeText(&esc, []byte(exe))
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc.String() + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`)
}

// newFileAutostart builds the file-based entry for an operating system.
// configHome is $XDG_CONFIG_HOME (Linux, may be empty) and home the user's
// home directory.
func newFileAutostart(goos, home, configHome, exe string) (Autostart, error) {
	switch goos {
	case "darwin":
		return fileAutostart{path: filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), content: launchAgentPlist(exe)}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		return fileAutostart{path: filepath.Join(configHome, "autostart", "aotus.desktop"), content: desktopEntry(exe)}, nil
	}
	return nil, fmt.Errorf("lifecycle: autostart is not supported on %s", goos)
}
