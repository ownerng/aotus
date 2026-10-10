package lifecycle

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/proc"
)

const childEnv = "AOTUS_LIFECYCLE_CHILD"

// TestMain lets the test binary act as a "daemon" that takes the lock and then
// dies without releasing it, the way a crash would.
func TestMain(m *testing.M) {
	if root := os.Getenv(childEnv); root != "" {
		l := datadir.Layout{Root: root}
		if _, err := Acquire(l); err != nil {
			os.Exit(2)
		}
		println("locked")
		os.Exit(3) // no Release: simulates a crash
	}
	os.Exit(m.Run())
}

func layout(t *testing.T) datadir.Layout {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSecondInstanceIsRejected(t *testing.T) {
	l := layout(t)
	first, err := Acquire(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteDiscovery(l, Discovery{PID: os.Getpid(), Address: "127.0.0.1:4321", StartedAt: time.Now(), Version: "test"}); err != nil {
		t.Fatal(err)
	}

	second, err := Acquire(l)
	if second != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Acquire = %v, %v; want ErrAlreadyRunning", second, err)
	}
	for _, want := range []string{strconv.Itoa(os.Getpid()), "127.0.0.1:4321"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q must point to the running daemon (missing %q)", err, want)
		}
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release must be repeatable: %v", err)
	}
	third, err := Acquire(l)
	if err != nil {
		t.Fatalf("after Release the lock is free: %v", err)
	}
	_ = third.Release()
}

func TestStaleLockIsReclaimed(t *testing.T) {
	l := layout(t)

	// A lock file with a dead PID in it, as an old daemon left it.
	if err := os.WriteFile(l.Lock(), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := Acquire(l)
	if err != nil {
		t.Fatalf("a leftover lock file must not block the start: %v", err)
	}
	if b, _ := os.ReadFile(l.Lock()); strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file = %q, want our PID", b)
	}
	_ = k.Release()

	// A real crash: a process takes the lock and dies without releasing it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := []string{childEnv + "=" + l.Root}
	if runtime.GOOS == "windows" {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	child, err := proc.Start(ctx, proc.Spec{Path: os.Args[0], Env: env})
	if err != nil {
		t.Fatal(err)
	}
	_ = child.Stdin().Close()
	for range child.Lines() {
	}
	if exit := child.Wait(); exit.Code != 3 {
		t.Fatalf("the crashing child exited with %+v, want code 3 (it must have taken the lock first)", exit)
	}
	k, err = Acquire(l)
	if err != nil {
		t.Fatalf("the lock of a crashed daemon must be reclaimed: %v", err)
	}
	_ = k.Release()
}

func TestDiscoveryFileIsOwnerOnly(t *testing.T) {
	l := layout(t)
	if _, err := ReadDiscovery(l); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("no file = %v, want ErrNoDaemon", err)
	}
	want := Discovery{PID: 4242, Address: "127.0.0.1:5555", StartedAt: time.Now().UTC().Truncate(time.Millisecond), Version: "1.2.3"}
	if err := WriteDiscovery(l, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDiscovery(l)
	if err != nil || got.PID != want.PID || got.Address != want.Address || got.Version != want.Version || !got.StartedAt.Equal(want.StartedAt) {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(l.Discovery())
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("discovery file mode = %o, want 600", fi.Mode().Perm())
		}
	}
	// Rewriting replaces the file atomically and leaves nothing behind.
	want.Address = "127.0.0.1:6666"
	if err := WriteDiscovery(l, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadDiscovery(l); got.Address != "127.0.0.1:6666" {
		t.Fatalf("rewrite lost: %+v", got)
	}
	entries, _ := os.ReadDir(l.Root)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temporary file %s", e.Name())
		}
	}
	if err := RemoveDiscovery(l); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDiscovery(l); err != nil {
		t.Fatalf("removing twice must be harmless: %v", err)
	}
	if _, err := ReadDiscovery(l); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("after removal = %v", err)
	}
	if err := os.WriteFile(l.Discovery(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiscovery(l); err == nil || errors.Is(err, ErrNoDaemon) {
		t.Fatalf("a damaged file is an error of its own, got %v", err)
	}
}

func TestAutostartEntryRoundTrip(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "My Apps", `aotusd "x"`)
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			home := t.TempDir()
			a, err := newFileAutostart(goos, home, "", exe)
			if err != nil {
				t.Fatal(err)
			}
			if on, err := a.Enabled(); err != nil || on {
				t.Fatalf("before Enable: %v, %v", on, err)
			}
			if err := a.Enable(); err != nil {
				t.Fatal(err)
			}
			if err := a.Enable(); err != nil {
				t.Fatalf("enabling twice must be harmless: %v", err)
			}
			if on, _ := a.Enabled(); !on {
				t.Fatal("after Enable it must report enabled")
			}
			fa := a.(fileAutostart)
			b, err := os.ReadFile(fa.path)
			if err != nil {
				t.Fatal(err)
			}
			switch goos {
			case "linux":
				if !strings.HasSuffix(fa.path, filepath.Join(".config", "autostart", "aotus.desktop")) {
					t.Errorf("path = %s", fa.path)
				}
				if !strings.Contains(string(b), `Exec="`) || !strings.Contains(string(b), `\"x\"`) || !strings.Contains(string(b), "My Apps") {
					t.Errorf("the Exec line must quote the path and escape its quotes:\n%s", b)
				}
			case "darwin":
				if !strings.HasSuffix(fa.path, filepath.Join("LaunchAgents", "dev.aotus.daemon.plist")) {
					t.Errorf("path = %s", fa.path)
				}
				var doc struct{ XMLName xml.Name }
				if err := xml.Unmarshal(b, &doc); err != nil || doc.XMLName.Local != "plist" {
					t.Errorf("the property list must be well-formed XML: %v\n%s", err, b)
				}
				if !strings.Contains(string(b), "&#34;x&#34;") && !strings.Contains(string(b), "&quot;x&quot;") {
					t.Errorf("the executable path must be XML-escaped:\n%s", b)
				}
			}
			if err := a.Disable(); err != nil {
				t.Fatal(err)
			}
			if err := a.Disable(); err != nil {
				t.Fatalf("disabling twice must be harmless: %v", err)
			}
			if on, _ := a.Enabled(); on {
				t.Fatal("after Disable it must report disabled")
			}
		})
	}
	// $XDG_CONFIG_HOME is honored on Linux.
	a, _ := newFileAutostart("linux", "/home/u", "/custom/config", exe)
	if got := a.(fileAutostart).path; got != filepath.Join("/custom/config", "autostart", "aotus.desktop") {
		t.Errorf("path = %s", got)
	}
	if _, err := newFileAutostart("plan9", "/h", "", exe); err == nil {
		t.Error("an unsupported system must be reported, not guessed")
	}
	if _, err := NewAutostart("relative/aotusd"); err == nil {
		t.Error("autostart needs an absolute path")
	}
}

func TestDesktopExecEscaping(t *testing.T) {
	for in, want := range map[string]string{
		`/usr/bin/aotusd`:    `"/usr/bin/aotusd"`,
		`/a b/aotusd`:        `"/a b/aotusd"`,
		`/a"b`:               `"/a\"b"`,
		`/a$b` + "`" + `c\d`: `"/a\$b\` + "`" + `c\\d"`,
		`/100%/aotusd`:       `"/100%%/aotusd"`,
	} {
		if got := desktopExec(in); got != want {
			t.Errorf("desktopExec(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSystemdUnitText(t *testing.T) {
	user, err := Unit(UnitOptions{ExecPath: "/home/me/bin/aotusd", DataDir: "/home/me/.aotus", Tailnet: true, Port: "7843", Owner: "me@example.com", MemoryMax: "3G", MaxTurns: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Unit]", "[Service]", "[Install]",
		"After=network-online.target tailscaled.service",
		"ExecStart=/home/me/bin/aotusd --data-dir /home/me/.aotus --max-turns 10 --tailnet --tailnet-port 7843 --owner me@example.com",
		"Environment=PATH=%h/.local/bin:", "Restart=on-failure", "TimeoutStopSec=30", "KillMode=control-group",
		"NoNewPrivileges=yes", "MemoryMax=3G", "WantedBy=default.target",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("the user unit misses %q:\n%s", want, user)
		}
	}
	if strings.Contains(user, "User=") || strings.Contains(user, "ProtectSystem") {
		t.Errorf("a user unit must not name a user or protect the system:\n%s", user)
	}

	system, err := Unit(UnitOptions{Scope: ScopeSystem, User: "aotus", Group: "aotus", ExecPath: "/usr/local/bin/aotusd", DataDir: "/var/lib/aotus"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User=aotus", "Group=aotus", "ExecStart=/usr/local/bin/aotusd --data-dir /var/lib/aotus\n", "WantedBy=multi-user.target", "ProtectSystem=full", "RestrictSUIDSGID=yes"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system unit misses %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "--tailnet") {
		t.Errorf("no tailnet flags unless asked:\n%s", system)
	}

	// Spaces and specifiers in paths are quoted, not interpreted.
	odd, err := Unit(UnitOptions{ExecPath: "/opt/my apps/aotusd", DataDir: "/data/100%/x $y"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(odd, `ExecStart="/opt/my apps/aotusd" --data-dir "/data/100%%/x $$y"`) {
		t.Errorf("quoting:\n%s", odd)
	}

	for _, bad := range []UnitOptions{
		{ExecPath: "aotusd", DataDir: "/d"},
		{ExecPath: "/a", DataDir: "d"},
		{Scope: ScopeSystem, ExecPath: "/a", DataDir: "/d"},
		{Scope: "global", ExecPath: "/a", DataDir: "/d"},
		{ExecPath: "/a", DataDir: "/d", Tailnet: true, Port: "99999"},
		{ExecPath: "/a", DataDir: "/d", MemoryMax: "3G; rm -rf /"},
		{ExecPath: "/a", DataDir: "/d", MaxTurns: -1},
	} {
		if _, err := Unit(bad); !errors.Is(err, ErrBadUnitOption) {
			t.Errorf("%+v must be refused, got %v", bad, err)
		}
	}
}

func TestSystemdUnitHasNoSecrets(t *testing.T) {
	// A hostile value must not be able to add a line or a section.
	for _, o := range []UnitOptions{
		{ExecPath: "/a", DataDir: "/d\nExecStartPre=/bin/evil"},
		{ExecPath: "/a\n[Service]", DataDir: "/d"},
		{ExecPath: "/a", DataDir: "/d", Tailnet: true, Owner: "x@example.com\nEnvironment=TOKEN=1"},
		{Scope: ScopeSystem, User: "root\nExecStart=/bin/sh", ExecPath: "/a", DataDir: "/d"},
	} {
		if text, err := Unit(o); !errors.Is(err, ErrBadUnitOption) {
			t.Errorf("%+v must be refused, got %q, %v", o, text, err)
		}
	}
	// A normal unit carries only paths and public settings.
	text, err := Unit(UnitOptions{ExecPath: "/usr/local/bin/aotusd", DataDir: "/home/me/.aotus", Tailnet: true, Owner: "me@example.com", TailscaleSocket: "/var/run/tailscale/tailscaled.sock"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		low := strings.ToLower(line)
		for _, forbidden := range []string{"token", "password", "secret", "apikey", "api_key", "authkey", "bearer"} {
			if strings.Contains(low, forbidden) {
				t.Errorf("the unit mentions %q: %s", forbidden, line)
			}
		}
		if strings.HasPrefix(line, "Environment=") && !strings.HasPrefix(line, "Environment=PATH=") {
			t.Errorf("the only environment the unit sets is PATH, got %s", line)
		}
	}

	// Writing and removing the file.
	dir := filepath.Join(t.TempDir(), "units")
	path, err := WriteUnit(dir, text)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != text {
		t.Fatal("the unit file was not written as rendered")
	}
	if _, err := RemoveUnit(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the unit file must be gone")
	}
	if _, err := RemoveUnit(dir); err != nil {
		t.Fatalf("removing twice must be harmless: %v", err)
	}
}
