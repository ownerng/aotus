package client_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/api"
	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/netaccess"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// remoteDaemon is the real API behind the real identification: connections to
// its listener are identified by a fixed table, as the Tailscale would, and no
// token is involved.
type remoteDaemon struct {
	addr string
	st   *store.Store
	ln   net.Listener
}

func startRemote(t *testing.T, who netaccess.StaticIdentifier, owner string) *remoteDaemon {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if owner != "" {
		if err := st.SetOwnerLogin(context.Background(), owner); err != nil {
			t.Fatal(err)
		}
	}
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	h := api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: "unused-on-the-tailnet", Version: "test", Store: st, TailnetHosts: []string{"127.0.0.1", "localhost"}})
	var lc net.ListenConfig
	raw, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := netaccess.IdentifyListener(raw, who, nil)
	srv := &http.Server{Handler: h, ConnContext: netaccess.ConnContext, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &remoteDaemon{addr: raw.Addr().String(), st: st, ln: raw}
}

func TestDialSavedConnection(t *testing.T) {
	ctx := context.Background()
	d := startRemote(t, netaccess.StaticIdentifier{"127.0.0.1": {Login: "Owner@Example.com", Device: "laptop"}}, "owner@example.com")

	path := filepath.Join(t.TempDir(), "cfg", "connections.json")
	var saved client.Connections
	if err := saved.Add("vps", d.addr); err != nil {
		t.Fatal(err)
	}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := client.LoadConnections(path)
	if err != nil {
		t.Fatal(err)
	}
	if conn, ok := loaded.Get("VPS"); !ok || conn.Address != d.addr {
		t.Fatalf("loaded = %+v", loaded)
	}

	c, err := client.Connect(ctx, datadir.Layout{Root: t.TempDir()}, path, "vps")
	if err != nil {
		t.Fatal(err)
	}
	if c.Remote() != "vps" {
		t.Fatalf("Remote() = %q", c.Remote())
	}
	me, err := c.Me(ctx)
	if err != nil || me.Login != "Owner@Example.com" || me.Role != "owner" || me.Method != "tailnet" || me.Device != "laptop" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	// The whole API works over the connection, with no token.
	if _, err := c.Profiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.AllowLogin(ctx, "ana@example.com", false); !client.HasCode(err, "acknowledgement_required") {
		t.Fatalf("allowing without the acknowledgement: %v", err)
	}
	if err := c.AllowLogin(ctx, "ana@example.com", true); err != nil {
		t.Fatal(err)
	}
	acc, err := c.Access(ctx)
	if err != nil || acc.Owner != "owner@example.com" || len(acc.Entries) != 1 || acc.SharingNotice == "" {
		t.Fatalf("Access = %+v, %v", acc, err)
	}
	if err := c.DenyLogin(ctx, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	// A name that is not saved is a clear error; "local" and "" mean this computer.
	if _, err := client.Connect(ctx, datadir.Layout{Root: t.TempDir()}, path, "nope"); err == nil || !strings.Contains(err.Error(), "no saved connection") {
		t.Fatalf("unknown connection: %v", err)
	}
	if _, err := client.Connect(ctx, datadir.Layout{Root: t.TempDir()}, path, "local"); !errors.Is(err, client.ErrNoDaemon) {
		t.Fatalf("local with no daemon: %v", err)
	}
}

// severable is a TCP proxy whose connections can all be cut at once, like a
// link that drops, while it keeps accepting new ones.
type severable struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	down  bool
}

func newSeverable(t *testing.T, target string) *severable {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &severable{ln: ln}
	t.Cleanup(func() { _ = ln.Close(); p.cut() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.down {
				p.mu.Unlock()
				_ = c.Close()
				continue
			}
			var d net.Dialer
			up, err := d.DialContext(context.Background(), "tcp", target)
			if err != nil {
				p.mu.Unlock()
				_ = c.Close()
				continue
			}
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
	return p
}

func (p *severable) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *severable) setDown(v bool) { p.mu.Lock(); p.down = v; p.mu.Unlock() }

func TestReconnectsAfterDroppedLink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d := startRemote(t, netaccess.StaticIdentifier{"127.0.0.1": {Login: "owner@example.com", Device: "laptop"}}, "owner@example.com")
	proxy := newSeverable(t, d.addr)
	c, err := client.Dial(ctx, client.Connection{Name: "vps", Address: proxy.ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}

	// A kept-alive HTTP connection that the link killed: the next read works.
	if _, err := c.Profiles(ctx); err != nil {
		t.Fatal(err)
	}
	proxy.cut()
	if _, err := c.Profiles(ctx); err != nil {
		t.Fatalf("a read after the link dropped must retry and succeed: %v", err)
	}

	// The event stream comes back by itself, and says it was down.
	var mu sync.Mutex
	var states []bool
	hellos := make(chan struct{}, 8)
	followCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Follow(followCtx, "", func(m client.Message) {
			if m.Type == "hello" {
				hellos <- struct{}{}
			}
		}, func(up bool) { mu.Lock(); states = append(states, up); mu.Unlock() })
	}()
	waitHello := func(what string) {
		select {
		case <-hellos:
		case <-time.After(20 * time.Second):
			t.Fatalf("no hello %s", what)
		}
	}
	waitHello("at the start")
	proxy.setDown(true)
	proxy.cut()
	time.Sleep(600 * time.Millisecond) // a few failed attempts while the link is down
	proxy.setDown(false)
	waitHello("after the link came back")
	stop()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(states) < 3 || !states[0] || states[1] || !states[2] {
		t.Fatalf("states = %v, want up, down, up", states)
	}
}

func TestRemoteErrorsAreActionable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Not on the allow-list.
	d := startRemote(t, netaccess.StaticIdentifier{"127.0.0.1": {Login: "eve@example.com", Device: "x"}}, "owner@example.com")
	_, err := client.Dial(ctx, client.Connection{Name: "vps", Address: d.addr})
	if !errors.Is(err, client.ErrNotAllowed) || !strings.Contains(err.Error(), "aotus access allow") || !strings.Contains(err.Error(), "vps") {
		t.Fatalf("not allowed: %v", err)
	}

	// A name that does not resolve: Tailscale is not running here, or the name is wrong.
	_, err = client.Dial(ctx, client.Connection{Name: "gone", Address: "no-such-machine.invalid:7843"})
	if !errors.Is(err, client.ErrTailnetDown) || !strings.Contains(err.Error(), "Tailscale") {
		t.Fatalf("tailnet down: %v", err)
	}

	// Nothing listens there.
	var lc net.ListenConfig
	ln, _ := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	_ = ln.Close()
	_, err = client.Dial(ctx, client.Connection{Name: "off", Address: closed})
	if !errors.Is(err, client.ErrUnreachable) || !strings.Contains(err.Error(), "--tailnet") {
		t.Fatalf("daemon off: %v", err)
	}

	// The daemon closes the connection (what it does to a peer it cannot identify).
	d2 := startRemote(t, netaccess.StaticIdentifier{}, "owner@example.com")
	_, err = client.Dial(ctx, client.Connection{Name: "unknown", Address: d2.addr})
	if !errors.Is(err, client.ErrUnreachable) || !strings.Contains(err.Error(), "closed the connection") {
		t.Fatalf("unidentified: %v", err)
	}
}

func TestConnectionsFileHasNoSecrets(t *testing.T) {
	var c client.Connections
	for _, bad := range []string{
		"https://vps.example.ts.net:7843", "user:secret@vps.example.ts.net:7843", "vps.example.ts.net:7843/api",
		"vps.example.ts.net:7843?token=abc", "vps.example.ts.net", ":7843", "vps.example.ts.net:notaport", "vps ts.net:7843",
	} {
		if err := c.Add("vps", bad); err == nil {
			t.Errorf("Add accepted %q", bad)
		}
	}
	for _, bad := range []string{"", "local", "LOCAL", "a/b"} {
		if err := c.Add(bad, "vps.example.ts.net:7843"); err == nil {
			t.Errorf("Add accepted the name %q", bad)
		}
	}
	if err := c.Add("vps", "vps.tail1234.ts.net:7843"); err != nil {
		t.Fatal(err)
	}
	c.Active = "vps"
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	for _, forbidden := range []string{"token", "secret", "password", "key", "Bearer"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("the connections file mentions %q:\n%s", forbidden, text)
		}
	}
	if fi, err := os.Stat(path); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Errorf("the file must be owner-only: %v %v", fi, err)
	}
	if !c.Remove("VPS") || c.Active != "" {
		t.Errorf("removing the active connection must make this computer active: %+v", c)
	}
}
