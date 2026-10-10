package netaccess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestNoOptionListensOnAWildcard(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "[::]:0", ":0", "192.168.1.5:0", "8.8.8.8:0", "example.com:80"} {
		if l, err := Listen(addr); err == nil {
			_ = l.Close()
			t.Errorf("Listen(%q) must be refused", addr)
		}
	}
	for _, s := range []string{"0.0.0.0", "::", "127.0.0.1", "192.168.1.5", "10.0.0.1", "8.8.8.8", "100.63.255.255", "100.128.0.0", "fd7a:115c:a1e1::1", "::ffff:8.8.8.8"} {
		ip := netip.MustParseAddr(s)
		if l, err := ListenTailnet(ip, "0", StaticIdentifier{}, nil); err == nil {
			_ = l.Close()
			t.Errorf("ListenTailnet(%s) must be refused: it is not a tailnet address", s)
		} else if !errors.Is(err, ErrNotTailnet) {
			t.Errorf("ListenTailnet(%s) = %v, want ErrNotTailnet", s, err)
		}
	}
	for _, s := range []string{"100.64.0.1", "100.127.255.254", "fd7a:115c:a1e0::1", "fd7a:115c:a1e0:ab12:4843:cd96:6266:1234", "::ffff:100.100.1.1"} {
		if err := CheckTailnetAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s is a tailnet address: %v", s, err)
		}
	}
	if k, err := ParseKind(""); err != nil || k != KindLoopback {
		t.Errorf("the default kind is loopback, got %q, %v", k, err)
	}
	for _, bad := range []string{"any", "public", "0.0.0.0", "all"} {
		if _, err := ParseKind(bad); err == nil {
			t.Errorf("ParseKind(%q) must be refused", bad)
		}
	}
	if _, err := ListenTailnet(netip.MustParseAddr("100.64.0.1"), "0", nil, nil); err == nil {
		t.Error("a tailnet listener without an identifier must be refused")
	}
}

func TestTailnetListenerRefusesUnidentifiedPeer(t *testing.T) {
	ctx := context.Background()
	// The test connects from 127.0.0.1, which the identifier does not know.
	l, err := listenIdentified("127.0.0.1:0", StaticIdentifier{"10.9.9.9": {Login: "x@example.com"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("the daemon must close a connection it cannot identify, not talk to it")
	}
	select {
	case <-accepted:
		t.Fatal("an unidentified peer must never be returned by Accept")
	default:
	}

	// A device with no person (tag only) is refused too.
	l2, _ := listenIdentified("127.0.0.1:0", StaticIdentifier{"127.0.0.1": {Device: "build-box", Tags: []string{"tag:ci"}}}, nil)
	defer func() { _ = l2.Close() }()
	go func() { _, _ = l2.Accept() }()
	c2, err := d.DialContext(ctx, "tcp", l2.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c2.Read(make([]byte, 1)); err == nil {
		t.Fatal("a device with no person behind it must be refused")
	}
}

func TestCallerReachesHandlers(t *testing.T) {
	want := Caller{Login: "ana@example.com", Device: "laptop"}
	l, err := listenIdentified("127.0.0.1:0", StaticIdentifier{"127.0.0.1": want}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Caller, 1)
	srv := &http.Server{ConnContext: ConnContext, ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := CallerFrom(r.Context())
		if !ok {
			http.Error(w, "no caller", 500)
			return
		}
		got <- c
	})}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://"+l.Addr().String()+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	c := <-got
	if c.Login != want.Login || c.Device != want.Device || c.Method != MethodTailnet {
		t.Fatalf("caller = %+v, want %+v over the tailnet", c, want)
	}
	if s := c.String(); !strings.Contains(s, "ana@example.com") || !strings.Contains(s, "laptop") {
		t.Fatalf("String() = %q", s)
	}
	if _, ok := CallerFrom(context.Background()); ok {
		t.Fatal("a plain context carries no caller")
	}
}
