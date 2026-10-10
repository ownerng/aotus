package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
	"aotus/internal/netaccess"
)

// fakeTS is a Tailscale with a fixed answer.
type fakeTS struct {
	status netaccess.TailnetStatus
	err    error
	who    netaccess.StaticIdentifier
}

func (f *fakeTS) Status(context.Context) (netaccess.TailnetStatus, error) { return f.status, f.err }
func (f *fakeTS) Identify(ctx context.Context, a string) (netaccess.Caller, error) {
	return f.who.Identify(ctx, a)
}

// syncBuf is a buffer the daemon goroutine writes and the test reads.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type tailRig struct {
	layout datadir.Layout
	out    *syncBuf
	errOut *syncBuf
	done   chan int
	exited chan struct{} // closed once the daemon returned, whoever read done
	cancel context.CancelFunc
	mu     sync.Mutex
	bound_ []netip.Addr // addresses the daemon asked to bind
}

// startTailnetDaemon runs the daemon with the fake Tailscale. Since no tailnet
// address exists on a test machine, the listener seam binds loopback, but
// through the real identification wrapper.
func startTailnetDaemon(t *testing.T, ts *fakeTS, extra ...string) *tailRig {
	t.Helper()
	r := &tailRig{layout: datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}, out: &syncBuf{}, errOut: &syncBuf{}, done: make(chan int, 1), exited: make(chan struct{})}
	opts := options{
		newTailscale: func(string) tailscale { return ts },
		listenTailnet: func(ip netip.Addr, port string, id netaccess.Identifier, log *slog.Logger) (net.Listener, error) {
			r.mu.Lock()
			r.bound_ = append(r.bound_, ip)
			r.mu.Unlock()
			if err := netaccess.CheckTailnetAddr(ip); err != nil {
				return nil, err
			}
			var lc net.ListenConfig
			l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			return netaccess.IdentifyListener(l, id, log), nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	args := append([]string{"--data-dir", r.layout.Root, "--tailnet"}, extra...)
	go func() { r.done <- runWith(ctx, args, r.out, r.errOut, opts); close(r.exited) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.exited:
		case <-time.After(10 * time.Second):
		}
	})
	return r
}

func (r *tailRig) bound() []netip.Addr {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]netip.Addr(nil), r.bound_...)
}

func (r *tailRig) discovery(t *testing.T) lifecycle.Discovery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d, err := lifecycle.ReadDiscovery(r.layout); err == nil {
			return d
		}
		select {
		case code := <-r.done:
			t.Fatalf("the daemon exited with %d: %s", code, r.errOut.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("the daemon never published its address")
	return lifecycle.Discovery{}
}

func runningStatus() netaccess.TailnetStatus {
	return netaccess.TailnetStatus{
		Running: true, State: "Running", OwnerLogin: "Owner@Example.com",
		Addrs: []netip.Addr{netip.MustParseAddr("100.101.102.103")},
		Names: []string{"vps.tail1234.ts.net", "vps", "100.101.102.103"},
	}
}

// getHost sends a request to addr with a chosen Host header.
func getHost(t *testing.T, addr, host, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+path, nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTailnetFlagStartsBothListeners(t *testing.T) {
	ts := &fakeTS{status: runningStatus(), who: netaccess.StaticIdentifier{"127.0.0.1": {Login: "owner@example.com", Device: "laptop"}}}
	r := startTailnetDaemon(t, ts)
	d := r.discovery(t)

	if !strings.HasPrefix(d.Address, "127.0.0.1:") {
		t.Fatalf("loopback address = %q", d.Address)
	}
	if !strings.HasPrefix(d.Tailnet, "vps.tail1234.ts.net:") {
		t.Fatalf("the discovery file must publish the tailnet name, got %q", d.Tailnet)
	}
	if len(r.bound()) != 1 || r.bound()[0] != netip.MustParseAddr("100.101.102.103") {
		t.Fatalf("bound %v, want exactly the tailnet address", r.bound())
	}
	if !strings.Contains(r.out.String(), "owner of this daemon is owner@example.com") {
		t.Errorf("output = %q", r.out.String())
	}

	// Loopback needs the token.
	tokenBytes, _ := os.ReadFile(r.layout.Token())
	token := strings.TrimSpace(string(tokenBytes))
	if code, _ := get(t, "http://"+d.Address+"/api/v1/status", ""); code != http.StatusUnauthorized {
		t.Errorf("loopback without a token: %d", code)
	}
	if code, _ := get(t, "http://"+d.Address+"/api/v1/status", token); code != http.StatusOK {
		t.Errorf("loopback with the token: %d", code)
	}

	// The tailnet listener serves the identified owner with no token, under
	// the daemon's tailnet name, and refuses another Host.
	tailAddr := d.Tailnet
	tailAddr = strings.Replace(tailAddr, "vps.tail1234.ts.net", "127.0.0.1", 1) // the test's listener is on loopback
	if code, body := getHost(t, tailAddr, d.Tailnet, "/api/v1/me"); code != http.StatusOK || !strings.Contains(body, `"role":"owner"`) || !strings.Contains(body, "laptop") {
		t.Errorf("tailnet /me = %d %s", code, body)
	}
	if code, _ := getHost(t, tailAddr, "evil.example.com", "/api/v1/me"); code != http.StatusForbidden {
		t.Errorf("tailnet with a foreign Host: %d, want 403", code)
	}

	// The owner is recorded and survives: a restart without the flag keeps it.
	r.cancel()
	if code := <-r.done; code != 0 {
		t.Fatalf("exit code %d, errors: %s", code, r.errOut.String())
	}
	if _, err := lifecycle.ReadDiscovery(r.layout); err == nil {
		t.Error("the discovery file must be removed on shutdown")
	}
	// Shutdown closed the tailnet listener too.
	dialer := net.Dialer{Timeout: time.Second}
	if c, err := dialer.DialContext(context.Background(), "tcp", tailAddr); err == nil {
		_ = c.Close()
		t.Error("the tailnet listener is still open after shutdown")
	}
}

func TestOwnerFlagOverridesAndStays(t *testing.T) {
	ts := &fakeTS{status: runningStatus(), who: netaccess.StaticIdentifier{"127.0.0.1": {Login: "boss@example.com", Device: "pc"}}}
	r := startTailnetDaemon(t, ts, "--owner", "Boss@Example.com")
	d := r.discovery(t)
	tailAddr := strings.Replace(d.Tailnet, "vps.tail1234.ts.net", "127.0.0.1", 1)
	if code, body := getHost(t, tailAddr, d.Tailnet, "/api/v1/me"); code != http.StatusOK || !strings.Contains(body, "boss@example.com") {
		t.Fatalf("--owner must win over the node's person: %d %s", code, body)
	}
}

func TestTailscaleMissingKeepsLoopback(t *testing.T) {
	cases := map[string]*fakeTS{
		"missing":  {err: errors.New("dial unix /var/run/tailscale/tailscaled.sock: connect: no such file or directory")},
		"stopped":  {status: netaccess.TailnetStatus{State: "Stopped"}},
		"no addr":  {status: netaccess.TailnetStatus{Running: true, State: "Running", OwnerLogin: "o@example.com"}},
		"tag only": {status: func() netaccess.TailnetStatus { s := runningStatus(); s.OwnerLogin = ""; s.Tagged = true; return s }()},
	}
	reasons := map[string]string{"missing": "no such file", "stopped": "Stopped", "no addr": "no tailnet address", "tag only": "--owner"}
	for name, ts := range cases {
		t.Run(name, func(t *testing.T) {
			r := startTailnetDaemon(t, ts)
			d := r.discovery(t)
			if d.Tailnet != "" || len(r.bound()) != 0 {
				t.Fatalf("no tailnet listener must exist: discovery %+v, bound %v", d, r.bound())
			}
			msg := r.errOut.String()
			if !strings.Contains(msg, "tailnet listener is not started") || !strings.Contains(msg, reasons[name]) || !strings.Contains(msg, "loopback keeps working") {
				t.Fatalf("the message must say why: %q", msg)
			}
			tokenBytes, _ := os.ReadFile(r.layout.Token())
			if code, _ := get(t, "http://"+d.Address+"/api/v1/status", strings.TrimSpace(string(tokenBytes))); code != http.StatusOK {
				t.Fatalf("loopback must keep working, got %d", code)
			}
		})
	}
}

func TestTailnetListenerNeverBindsElsewhere(t *testing.T) {
	for _, bad := range []string{"0.0.0.0", "127.0.0.1", "192.168.1.5", "8.8.8.8", "::"} {
		st := runningStatus()
		st.Addrs = []netip.Addr{netip.MustParseAddr(bad)}
		ts := &fakeTS{status: st}
		r := startTailnetDaemon(t, ts)
		d := r.discovery(t)
		if len(r.bound()) != 0 {
			t.Fatalf("Tailscale said %s: the daemon tried to bind %v", bad, r.bound())
		}
		if d.Tailnet != "" || !strings.Contains(r.errOut.String(), "not on the tailnet") {
			t.Fatalf("%s: discovery %+v, message %q", bad, d, r.errOut.String())
		}
		r.cancel()
		<-r.done
	}
}
