package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aotus/internal/lifecycle"
)

// recorder is a runCmd that records what would have been run.
type recorder struct {
	calls   []string
	failing map[string]bool // commands (by their first two words) that fail
	output  map[string]string
}

func (r *recorder) run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for k := range r.failing {
		if strings.Contains(line, k) {
			return "", errors.New("refused: " + k)
		}
	}
	for k, v := range r.output {
		if strings.Contains(line, k) {
			return v, nil
		}
	}
	return "", nil
}

func serviceOpts(r *recorder) options {
	o := defaultOptions()
	o.runCmd = r.run
	o.goos = "linux"
	o.currentUser = func() (string, error) { return "deploy", nil }
	return o
}

func runService(t *testing.T, o options, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runWith(context.Background(), args, &out, &errOut, o)
	return code, out.String(), errOut.String()
}

func TestServiceInstallAndUninstallFlags(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")

	// With --service-dir the files are only written: nothing is asked of systemd.
	rec := &recorder{}
	dir := filepath.Join(t.TempDir(), "units")
	code, out, errOut := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install", "--service-dir", dir, "--tailnet", "--owner", "me@example.com", "--tailnet-port", "7900")
	if code != 0 || len(rec.calls) != 0 {
		t.Fatalf("install into a directory: code %d, commands %v, err %q", code, rec.calls, errOut)
	}
	b, err := os.ReadFile(filepath.Join(dir, lifecycle.UnitName))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(b)
	exe, _ := os.Executable()
	for _, want := range []string{"ExecStart=" + exe + " --data-dir " + data + " --tailnet --tailnet-port 7900 --owner me@example.com", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit misses %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "--tailscale-socket") {
		t.Errorf("the default Tailscale socket must not be written into the unit:\n%s", unit)
	}
	if !strings.Contains(out, "wrote") {
		t.Errorf("output = %q", out)
	}
	if code, out, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "status", "--service-dir", dir); code != 0 || !strings.Contains(out, "installed at") {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, _, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "uninstall", "--service-dir", dir); code != 0 {
		t.Fatalf("uninstall from a directory: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, lifecycle.UnitName)); err == nil {
		t.Fatal("the unit file must be removed")
	}
	if code, out, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "status", "--service-dir", dir); code != 0 || !strings.Contains(out, "not installed") {
		t.Fatalf("status after removal: %d %q", code, out)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("a directory install must never run commands: %v", rec.calls)
	}

	// Normal install: unit in the user's config directory, then the service
	// manager is asked to load, enable and start it, and linger is turned on.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	rec = &recorder{}
	code, out, errOut = runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install")
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now aotusd.service", "loginctl enable-linger deploy"}
	if code != 0 || strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("install: code %d, commands %v, want %v (err %q)", code, rec.calls, want, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "cfg", "systemd", "user", lifecycle.UnitName)); err != nil {
		t.Fatalf("the unit must be in the user's systemd directory: %v", err)
	}
	if !strings.Contains(out, "linger is on") || !strings.Contains(out, "starts at boot") {
		t.Errorf("output = %q", out)
	}

	// Linger refused: still installed, with the command to run as an administrator.
	rec = &recorder{failing: map[string]bool{"enable-linger": true}}
	code, _, errOut = runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install")
	if code != 0 || !strings.Contains(errOut, "sudo loginctl enable-linger deploy") {
		t.Fatalf("linger refused: %d %q", code, errOut)
	}

	// A unit that was written but could not be started says so.
	rec = &recorder{failing: map[string]bool{"enable --now": true}}
	if code, _, errOut := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install"); code != 1 || !strings.Contains(errOut, "could not be started") {
		t.Fatalf("enable failing: %d %q", code, errOut)
	}

	// Uninstall disables first, removes the file, reloads; and leaves the data alone.
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	rec = &recorder{}
	code, out, _ = runService(t, serviceOpts(rec), "--data-dir", data, "--service", "uninstall")
	want = []string{"systemctl --user disable --now aotusd.service", "systemctl --user daemon-reload"}
	if code != 0 || strings.Join(rec.calls, "|") != strings.Join(want, "|") || !strings.Contains(out, "data directory is untouched") {
		t.Fatalf("uninstall: %d %v %q", code, rec.calls, out)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("uninstall must not touch the data directory")
	}
	rec = &recorder{output: map[string]string{"is-active": "active\n"}}
	if _, err := lifecycle.WriteUnit(filepath.Join(home, "cfg", "systemd", "user"), "x"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "status"); code != 0 || !strings.Contains(out, "state: active") {
		t.Fatalf("status: %d %q", code, out)
	}

	// System scope: needs a user, runs as it, uses systemctl without --user.
	rec = &recorder{}
	sysDir := filepath.Join(t.TempDir(), "etc")
	if code, _, errOut := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install", "--service-scope", "system", "--service-dir", sysDir); code != 1 || !strings.Contains(errOut, "--service-user") {
		t.Fatalf("system scope without a user: %d %q", code, errOut)
	}
	if code, _, errOut := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install", "--service-scope", "system", "--service-user", "deploy", "--service-dir", sysDir); code != 0 {
		t.Fatalf("system scope: %d %q", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(sysDir, lifecycle.UnitName)); !strings.Contains(string(b), "User=deploy") || !strings.Contains(string(b), "multi-user.target") {
		t.Fatalf("system unit:\n%s", b)
	}

	// Wrong usage, and the wrong operating system.
	if code, _, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "restart"); code != 2 {
		t.Errorf("unknown action: %d, want 2", code)
	}
	if code, _, _ := runService(t, serviceOpts(rec), "--data-dir", data, "--service", "install", "--service-scope", "galaxy"); code != 2 {
		t.Errorf("unknown scope: %d, want 2", code)
	}
	other := serviceOpts(rec)
	other.goos = "windows"
	if code, _, errOut := runService(t, other, "--data-dir", data, "--service", "install"); code != 1 || !strings.Contains(errOut, "Linux") || !strings.Contains(errOut, "--autostart") {
		t.Errorf("windows: %d %q", code, errOut)
	}
}
