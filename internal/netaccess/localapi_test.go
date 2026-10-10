package netaccess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTailscale serves the recorded fixtures on a Unix socket, like tailscaled
// does, and refuses what the real one refuses: any Host but local-tailscaled.sock.
type fakeTailscale struct {
	socket string
	hits   []string
}

func startFakeTailscale(t *testing.T, status string, whois map[string]string) *fakeTailscale {
	t.Helper()
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeTailscale{socket: filepath.Join(dir, "s")}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", f.socket)
	if err != nil {
		t.Skipf("Unix sockets are not available here: %v", err)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits = append(f.hits, r.Method+" "+r.URL.RequestURI())
		if r.Host != "local-tailscaled.sock" {
			http.Error(w, "invalid Host", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/localapi/v0/status":
			_, _ = w.Write(read(status))
		case "/localapi/v0/whois":
			file, ok := whois[r.URL.Query().Get("addr")]
			if !ok {
				http.Error(w, "no match for IP:port", http.StatusNotFound)
				return
			}
			_, _ = w.Write(read(file))
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func TestLocalAPIContract(t *testing.T) {
	ctx := context.Background()
	f := startFakeTailscale(t, "localapi-status.json", map[string]string{"100.64.0.9:51234": "localapi-whois-person.json"})
	api := NewLocalAPI(f.socket)

	st, err := api.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Tagged || st.OwnerLogin != "owner@example.com" {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Addrs) != 2 || st.Addrs[0] != netip.MustParseAddr("100.101.102.103") || !st.Addrs[1].Is6() {
		t.Fatalf("addresses = %v, want IPv4 first", st.Addrs)
	}
	for _, want := range []string{"vps.tail1234.ts.net", "vps", "100.101.102.103"} {
		found := false
		for _, n := range st.Names {
			found = found || n == want
		}
		if !found {
			t.Errorf("names %v miss %q (the trailing dot of the DNS name must go)", st.Names, want)
		}
	}
	for _, a := range st.Addrs {
		if err := CheckTailnetAddr(a); err != nil {
			t.Errorf("address %s from the status is not a tailnet address: %v", a, err)
		}
	}

	who, err := api.Identify(ctx, "100.64.0.9:51234")
	if err != nil {
		t.Fatal(err)
	}
	if who.Login != "Ana@Example.com" || who.Device != "laptop" || who.Method != MethodTailnet {
		t.Fatalf("caller = %+v", who)
	}
	if !strings.Contains(strings.Join(f.hits, "\n"), "/localapi/v0/whois?addr=100.64.0.9%3A51234") {
		t.Fatalf("whois was asked as %v", f.hits)
	}

	stopped := NewLocalAPI(startFakeTailscale(t, "localapi-status-stopped.json", nil).socket)
	if st, err := stopped.Status(ctx); err != nil || st.Running || st.State != "Stopped" || len(st.Addrs) != 0 {
		t.Fatalf("stopped status = %+v, %v", st, err)
	}
}

func TestWhoisRefusalIsAnUnidentifiedPeer(t *testing.T) {
	ctx := context.Background()
	f := startFakeTailscale(t, "localapi-status.json", nil)
	api := NewLocalAPI(f.socket)
	// Tailscale answers 404 for a peer it does not know.
	if _, err := api.Identify(ctx, "100.64.0.77:1"); !errors.Is(err, ErrUnidentified) {
		t.Fatalf("an unknown peer: %v, want ErrUnidentified", err)
	}
	// Tailscale not running at all.
	gone := NewLocalAPI(filepath.Join(t.TempDir(), "nothing.sock"))
	if _, err := gone.Identify(ctx, "100.64.0.9:1"); !errors.Is(err, ErrUnidentified) {
		t.Fatalf("no Tailscale: %v, want ErrUnidentified", err)
	}
	if _, err := gone.Status(ctx); !errors.Is(err, ErrTailscaleUnavailable) {
		t.Fatalf("no Tailscale: status error %v, want ErrTailscaleUnavailable", err)
	}
}

func TestTaggedDeviceIsRefused(t *testing.T) {
	ctx := context.Background()
	f := startFakeTailscale(t, "localapi-status-tagged.json", map[string]string{"100.64.0.20:5": "localapi-whois-tagged.json"})
	api := NewLocalAPI(f.socket)
	_, err := api.Identify(ctx, "100.64.0.20:5")
	if !errors.Is(err, ErrUnidentified) || !strings.Contains(err.Error(), "tag:ci") {
		t.Fatalf("a tagged device: %v, want ErrUnidentified naming the tag", err)
	}
	st, err := api.Status(ctx)
	if err != nil || !st.Tagged || st.OwnerLogin != "" {
		t.Fatalf("a tagged node has no owner login: %+v, %v", st, err)
	}
}
