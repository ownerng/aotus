//go:build desktop

package main

import (
	"context"
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
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// remoteDaemon is a daemon reached the way a VPS is: its listener identifies
// the peer (here by a fixed table, in production through Tailscale) and no
// token is used.
type remoteDaemon struct {
	addr string
	st   *store.Store
}

func startRemoteDaemon(t *testing.T, login string) *remoteDaemon {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetOwnerLogin(context.Background(), login); err != nil {
		t.Fatal(err)
	}
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	h := api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: "unused", Version: "remote-test", Store: st, TailnetHosts: []string{"127.0.0.1"}})
	var lc net.ListenConfig
	raw, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	id := netaccess.StaticIdentifier{"127.0.0.1": {Login: login, Device: "this-pc"}}
	srv := &http.Server{Handler: h, ConnContext: netaccess.ConnContext, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(netaccess.IdentifyListener(raw, id, nil)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &remoteDaemon{addr: raw.Addr().String(), st: st}
}

// saveConnections writes a connections file with the given remote daemons and
// the active one.
func saveConnections(t *testing.T, active string, conns ...client.Connection) string {
	t.Helper()
	path := connFile(t)
	c := client.Connections{Active: active}
	for _, x := range conns {
		if err := c.Add(x.Name, x.Address); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func neverStart(t *testing.T) func(string, ...string) (int, error) {
	return func(string, ...string) (int, error) {
		t.Error("the app started a local daemon while a remote connection was active")
		return 0, nil
	}
}

func TestRemoteActiveNeverStartsLocalDaemon(t *testing.T) {
	d := startRemoteDaemon(t, "owner@example.com")
	path := saveConnections(t, "vps", client.Connection{Name: "vps", Address: d.addr})
	b := NewBackend(Options{ConnectionsPath: path, Layout: newLayout(t), StartDaemon: neverStart(t), DaemonPath: "/opt/aotusd"})
	defer b.shutdown()

	s, err := b.Connect()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Connected || !s.Remote || s.Connection != "vps" || s.Login != "owner@example.com" || s.Role != "owner" || s.Started || s.Version != "remote-test" {
		t.Fatalf("state = %+v", s)
	}

	// Even when the remote cannot be reached, the answer is an error, not a daemon on this PC.
	var lc net.ListenConfig
	ln, _ := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	path2 := saveConnections(t, "off", client.Connection{Name: "off", Address: dead})
	b2 := NewBackend(Options{ConnectionsPath: path2, Layout: newLayout(t), StartDaemon: neverStart(t), DaemonPath: "/opt/aotusd"})
	defer b2.shutdown()
	s2, err := b2.Connect()
	if err == nil || s2.Connected || !s2.Remote || s2.Connection != "off" || !strings.Contains(s2.Error, "--tailnet") {
		t.Fatalf("unreachable remote: %+v, %v", s2, err)
	}
}

func TestSwitchingConnectionsReloadsEverything(t *testing.T) {
	l := newLayout(t)
	startFakeDaemon(t, l)
	remote := startRemoteDaemon(t, "owner@example.com")
	path := saveConnections(t, "", client.Connection{Name: "vps", Address: remote.addr})
	col := newCollector()
	b := NewBackend(Options{ConnectionsPath: path, Layout: l, Emit: col.emit})
	defer b.shutdown()

	if s, err := b.Connect(); err != nil || s.Remote || s.Connection != "local" {
		t.Fatalf("starts on this computer: %+v, %v", s, err)
	}
	prof, err := b.CreateProfile(client.NewProfile{Kind: "claude", Name: "local-main", Binary: os.Args[0], Mode: "terminal",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(providertest.TUI)}})
	if err != nil {
		t.Fatal(err)
	}
	emp, err := b.CreateEmployee(client.NewEmployee{Name: "Atlas", ProfileID: prof.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := b.OpenTerminal(emp.ID, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	infos, _ := b.Connections()
	if len(infos) != 2 || !infos[0].Active || infos[1].Name != "vps" || infos[1].Active || infos[1].Address != remote.addr {
		t.Fatalf("connections = %+v", infos)
	}

	// Switch to the remote: other data, no terminal of the old one, a reload is requested.
	if s, err := b.SwitchConnection("vps"); err != nil || !s.Remote || s.Connection != "vps" {
		t.Fatalf("switch: %+v, %v", s, err)
	}
	if es, err := b.Employees(); err != nil || len(es) != 0 {
		t.Fatalf("the remote has no employees yet: %v, %v", es, err)
	}
	if ps, _ := b.Profiles(); len(ps) != 0 {
		t.Fatalf("profiles of the old connection are still visible: %+v", ps)
	}
	if err := b.TerminalInput(key, "x"); err == nil {
		t.Fatal("a terminal of the old connection must be closed after switching")
	}
	col.waitFor(t, "a reload request", func(r recorded) bool { return r.name == EventResync })
	saved, _ := client.LoadConnections(path)
	if saved.Active != "vps" {
		t.Fatalf("the chosen connection must be remembered, active = %q", saved.Active)
	}
	infos, _ = b.Connections()
	if infos[0].Active || !infos[1].Active {
		t.Fatalf("connections after the switch = %+v", infos)
	}

	// Events of the new connection reach the window, and only those.
	rp, err := b.CreateProfile(client.NewProfile{Kind: "claude", Name: "vps-main", Binary: os.Args[0], Mode: "structured",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(providertest.Hello)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptNotice(rp.ID, "claude-headless"); err != nil {
		t.Fatal(err)
	}
	re, err := b.CreateEmployee(client.NewEmployee{Name: "Remy", ProfileID: rp.ID})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	turn, err := b.Send(re.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	col.waitFor(t, "the remote turn to end", func(r recorded) bool {
		u, ok := r.data.(*client.Update)
		return ok && u.TurnID == turn && u.Kind == "turn_ended"
	})

	// And back to this computer.
	if s, err := b.SwitchConnection("local"); err != nil || s.Remote {
		t.Fatalf("back: %+v, %v", s, err)
	}
	if es, _ := b.Employees(); len(es) != 1 || es[0].Name != "Atlas" {
		t.Fatalf("employees after coming back = %+v", es)
	}
	if saved, _ = client.LoadConnections(path); saved.Active != "" {
		t.Fatalf("active = %q, want this computer", saved.Active)
	}

	// A connection that cannot be reached leaves the window where it was.
	if err := b.AddConnection("ghost", "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SwitchConnection("ghost"); err == nil {
		t.Fatal("switching to an unreachable connection must fail")
	}
	if s := b.State(); !s.Connected || s.Remote {
		t.Fatalf("after a failed switch the window must be back on this computer: %+v", s)
	}
	if es, err := b.Employees(); err != nil || len(es) != 1 {
		t.Fatalf("still working on this computer: %v %v", es, err)
	}
	if tr := b.TestConnection("vps"); !tr.OK || tr.Role != "owner" || tr.Login != "owner@example.com" {
		t.Fatalf("TestConnection = %+v", tr)
	}
	if tr := b.TestConnection("ghost"); tr.OK || tr.Error == "" {
		t.Fatalf("TestConnection(ghost) = %+v", tr)
	}
	if err := b.RemoveConnection("ghost"); err != nil {
		t.Fatal(err)
	}
}

func TestStreamsChatFromRemoteDaemon(t *testing.T) {
	remote := startRemoteDaemon(t, "owner@example.com")
	path := saveConnections(t, "vps", client.Connection{Name: "vps", Address: remote.addr})
	col := newCollector()
	b := NewBackend(Options{ConnectionsPath: path, Layout: newLayout(t), Emit: col.emit, StartDaemon: neverStart(t), DaemonPath: "/opt/aotusd"})
	defer b.shutdown()
	if _, err := b.Connect(); err != nil {
		t.Fatal(err)
	}
	p, err := b.CreateProfile(client.NewProfile{Kind: "claude", Name: "main", Binary: os.Args[0], Mode: "structured",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(providertest.Hello)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptNotice(p.ID, "claude-headless"); err != nil {
		t.Fatal(err)
	}
	e, err := b.CreateEmployee(client.NewEmployee{Name: "Atlas", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	turnID, err := b.Send(e.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	col.waitFor(t, "the turn to end", func(r recorded) bool {
		u, ok := r.data.(*client.Update)
		if !ok || u.TurnID != turnID {
			return false
		}
		if u.Kind == "event" && u.Event != nil && u.Event.Kind == "text" {
			text.WriteString(u.Event.Text)
		}
		return u.Kind == "turn_ended"
	})
	if text.Len() == 0 {
		t.Fatal("the answer never streamed through the remote connection")
	}
	turns, err := b.History(e.ID, 10, "")
	if err != nil || len(turns) != 1 || turns[0].State != "completed" {
		t.Fatalf("history = %+v, %v", turns, err)
	}
	// The remote daemon's own record names who did it.
	rows, _ := remote.st.Audit(context.Background(), e.ID, 0)
	if len(rows) == 0 || !strings.Contains(rows[0].Caller, "owner@example.com") {
		t.Fatalf("audit rows = %+v, want the caller", rows)
	}
	// The terminal works over the remote connection too.
	tp, err := b.CreateProfile(client.NewProfile{Kind: "claude", Name: "tui", Binary: os.Args[0], Mode: "terminal",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(providertest.TUI)}})
	if err != nil {
		t.Fatal(err)
	}
	te, err := b.CreateEmployee(client.NewEmployee{Name: "Tia", ProfileID: tp.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := b.OpenTerminal(te.ID, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	col.waitFor(t, "the terminal banner", func(r recorded) bool {
		c, ok := r.data.(TerminalChunk)
		return ok && c.Key == key && c.Data != ""
	})
	if err := b.TerminalInput(key, "ping\r"); err != nil {
		t.Fatal(err)
	}
}

// severable is a TCP proxy whose connections can be cut, and which can refuse
// new ones, like a link that drops and comes back.
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

func TestDroppedLinkIsShownAndRecovered(t *testing.T) {
	remote := startRemoteDaemon(t, "owner@example.com")
	proxy := newSeverable(t, remote.addr)
	path := saveConnections(t, "vps", client.Connection{Name: "vps", Address: proxy.ln.Addr().String()})
	col := newCollector()
	b := NewBackend(Options{ConnectionsPath: path, Layout: newLayout(t), Emit: col.emit, StartDaemon: neverStart(t), DaemonPath: "/opt/aotusd"})
	defer b.shutdown()
	if _, err := b.Connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Employees(); err != nil {
		t.Fatal(err)
	}
	col.mu.Lock()
	before := len(col.events)
	col.mu.Unlock()

	// The link drops and stays down for a while: the window is told.
	proxy.setDown(true)
	proxy.cut()
	col.waitFor(t, "the connection to be reported lost", func(r recorded) bool {
		s, ok := r.data.(DaemonState)
		return ok && !s.Connected && s.Remote && s.Error != ""
	})
	if s := b.State(); s.Connected || s.Connection != "vps" {
		t.Fatalf("state while down = %+v", s)
	}
	// While it is down nothing asks the window to reload, so what is on screen stays.
	col.mu.Lock()
	for _, e := range col.events[before:] {
		if e.name == EventResync {
			t.Error("a reload was requested while the link was still down")
		}
	}
	col.mu.Unlock()

	// It comes back by itself: connected again, and one reload asks the window to catch up.
	proxy.setDown(false)
	col.waitFor(t, "the connection to be reported back", func(r recorded) bool {
		s, ok := r.data.(DaemonState)
		return ok && s.Connected && s.Remote
	})
	col.waitFor(t, "the reload request after the link came back", func(r recorded) bool { return r.name == EventResync })
	if _, err := b.Employees(); err != nil {
		t.Fatalf("the connection must work again: %v", err)
	}
}
