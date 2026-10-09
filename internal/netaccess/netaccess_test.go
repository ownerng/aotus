package netaccess

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aotus/internal/datadir"
)

func layout(t *testing.T) datadir.Layout {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestDefaultsToLoopback(t *testing.T) {
	for _, addr := range []string{"", DefaultAddress, "localhost:0", "[::1]:0", "127.0.0.2:0"} {
		l, err := Listen(addr)
		if err != nil {
			if addr == "[::1]:0" {
				t.Skipf("IPv6 loopback is not available here: %v", err)
			}
			if addr == "127.0.0.2:0" && runtime.GOOS == "darwin" {
				t.Logf("macOS only has 127.0.0.1 on its loopback interface: %v", err)
				continue
			}
			t.Fatalf("Listen(%q): %v", addr, err)
		}
		ip, _ := netip.ParseAddr(l.Addr().(*net.TCPAddr).IP.String())
		if !ip.IsLoopback() {
			t.Errorf("Listen(%q) bound %s, which is not loopback", addr, l.Addr())
		}
		if l.Addr().(*net.TCPAddr).Port == 0 {
			t.Errorf("Listen(%q) did not get a real port", addr)
		}
		_ = l.Close()
	}
}

func TestRefusesNonLoopbackBind(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:8080", ":8080", "[::]:8080", "192.168.1.10:8080", "10.0.0.5:0", "8.8.8.8:53",
		"example.com:80", "my-machine:80", "100.64.0.1:80", // a Tailscale-style address is not loopback either
		"127.0.0.1", "not an address", "[fe80::1%eth0]:80",
	} {
		if err := CheckBind(addr); err == nil {
			t.Errorf("CheckBind(%q) accepted an address that exposes the daemon", addr)
		}
		if l, err := Listen(addr); err == nil {
			_ = l.Close()
			t.Errorf("Listen(%q) opened a listener", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "127.0.0.1:8080", "localhost:9", "[::1]:80", "LOCALHOST:1"} {
		if err := CheckBind(addr); err != nil {
			t.Errorf("CheckBind(%q) = %v, want it accepted", addr, err)
		}
	}
	if err := CheckBind("0.0.0.0:1"); !errors.Is(err, ErrNonLoopback) {
		t.Errorf("err = %v, want ErrNonLoopback", err)
	}
}

func TestLocalTokenFilePermissions(t *testing.T) {
	l := layout(t)
	tok, err := LoadOrCreateToken(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 || strings.Trim(tok, "0123456789abcdef") != "" {
		t.Fatalf("token = %q, want 64 hex characters (256 bits)", tok)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(l.Token())
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("token file mode = %o, want 600", fi.Mode().Perm())
		}
		di, _ := os.Stat(l.Root)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("data directory mode = %o, want 700", di.Mode().Perm())
		}
		// A token file somebody loosened is tightened on the next load.
		if err := os.Chmod(l.Token(), 0o644); err != nil {
			t.Fatal(err)
		}
		again, err := LoadOrCreateToken(l)
		if err != nil || again != tok {
			t.Fatalf("reload = %q, %v", again, err)
		}
		if fi, _ := os.Stat(l.Token()); fi.Mode().Perm() != 0o600 {
			t.Fatalf("token file mode after reload = %o, want 600", fi.Mode().Perm())
		}
	}
	// No temporary files are left lying around, readable or not.
	entries, _ := os.ReadDir(l.Root)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temporary file %s", e.Name())
		}
	}
}

func TestTokenRegeneratedWhenMissing(t *testing.T) {
	l := layout(t)
	first, err := LoadOrCreateToken(l)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := LoadOrCreateToken(l); again != first {
		t.Fatal("an existing token must be reused, not replaced on every start")
	}

	if err := os.Remove(l.Token()); err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateToken(l)
	if err != nil || second == first || len(second) != 64 {
		t.Fatalf("after deleting the file: %q, %v; want a new valid token", second, err)
	}

	// A damaged file is replaced, not trusted.
	if err := os.WriteFile(l.Token(), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := LoadOrCreateToken(l)
	if err != nil || len(third) != 64 || third == second {
		t.Fatalf("after damage: %q, %v", third, err)
	}

	rotated, err := RotateToken(l)
	if err != nil || rotated == third {
		t.Fatalf("rotate = %q, %v", rotated, err)
	}
	if got, _ := LoadOrCreateToken(l); got != rotated {
		t.Fatal("the rotated token must be the one on disk")
	}
	if !TokenValid(rotated, rotated) || TokenValid("wrong", rotated) || TokenValid("", "") || TokenValid(rotated[:63], rotated) {
		t.Fatal("TokenValid must accept only the exact token, and never an empty one")
	}
}
