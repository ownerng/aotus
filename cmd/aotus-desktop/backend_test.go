//go:build desktop

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/api"
	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

const token = "desktop-test-token-0123456789abcdef0123456789abcdef0123456789abcd"

func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}

// fakeDaemon is the real API over real storage, published the way aotusd does.
type fakeDaemon struct {
	layout datadir.Layout
	srv    *httptest.Server
	mgr    *orchestrator.Manager
	st     *store.Store
}

func newLayout(t *testing.T) datadir.Layout {
	t.Helper()
	return datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
}

// start brings the daemon up on the layout.
func startFakeDaemon(t *testing.T, l datadir.Layout) *fakeDaemon {
	t.Helper()
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	srv := httptest.NewServer(api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: token, Version: "test"}))
	if err := os.WriteFile(l.Token(), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.WriteDiscovery(l, lifecycle.Discovery{PID: os.Getpid(), Address: strings.TrimPrefix(srv.URL, "http://"), StartedAt: time.Now(), Version: "test"}); err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{layout: l, srv: srv, mgr: mgr, st: st}
	t.Cleanup(d.stop)
	return d
}

func (d *fakeDaemon) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d.srv.Close()
	_ = d.mgr.Shutdown(ctx)
	_ = d.st.Close()
}

// collector records the events the window would receive.
type collector struct {
	mu     sync.Mutex
	events []recorded
	wake   chan struct{}
}

type recorded struct {
	name string
	data any
}

func newCollector() *collector { return &collector{wake: make(chan struct{}, 1)} }

func (c *collector) emit(name string, data any) {
	c.mu.Lock()
	c.events = append(c.events, recorded{name, data})
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// waitFor returns once an event satisfies match, or fails the test.
func (c *collector) waitFor(t *testing.T, what string, match func(recorded) bool) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	seen := 0
	for {
		c.mu.Lock()
		batch := append([]recorded(nil), c.events[seen:]...)
		seen = len(c.events)
		c.mu.Unlock()
		for _, e := range batch {
			if match(e) {
				return
			}
		}
		select {
		case <-c.wake:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestStartsDaemonWhenNoneIsRunning(t *testing.T) {
	l := newLayout(t)
	var d *fakeDaemon
	var gotPath string
	var gotArgs []string
	b := NewBackend(Options{
		Layout:     l,
		DaemonPath: "/opt/aotus/aotusd",
		StartDaemon: func(path string, args ...string) (int, error) {
			gotPath, gotArgs = path, args
			d = startFakeDaemon(t, l) // what the real aotusd does once started
			return 1234, nil
		},
		ReadyTimeout: 5 * time.Second,
	})
	defer b.shutdown()

	s, err := b.Connect()
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || !s.Connected || !s.Started || s.Version != "test" {
		t.Fatalf("state %+v, daemon started: %v", s, d != nil)
	}
	if gotPath != "/opt/aotus/aotusd" || len(gotArgs) != 2 || gotArgs[0] != "--data-dir" || gotArgs[1] != l.Root {
		t.Fatalf("the daemon was started as %s %v", gotPath, gotArgs)
	}
	if _, err := b.Employees(); err != nil {
		t.Fatalf("the daemon started by the app does not answer: %v", err)
	}
}

func TestReportsAMissingDaemonBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	b := NewBackend(Options{Layout: newLayout(t), ReadyTimeout: time.Second})
	defer b.shutdown()
	s, err := b.Connect()
	if err == nil || s.Connected || !strings.Contains(s.Error, "aotusd") {
		t.Fatalf("Connect = %+v, %v; want an error that names aotusd", s, err)
	}
}

func TestReusesRunningDaemon(t *testing.T) {
	l := newLayout(t)
	startFakeDaemon(t, l)
	started := false
	b := NewBackend(Options{Layout: l, StartDaemon: func(string, ...string) (int, error) { started = true; return 0, nil }})
	defer b.shutdown()

	s, err := b.Connect()
	if err != nil {
		t.Fatal(err)
	}
	if started || !s.Connected || s.Started {
		t.Fatalf("a running daemon must be reused: started=%v state=%+v", started, s)
	}
	if again, err := b.Connect(); err != nil || again != s {
		t.Fatalf("connecting twice: %+v, %v", again, err)
	}
}

func TestClosingWindowKeepsDaemonRunning(t *testing.T) {
	l := newLayout(t)
	d := startFakeDaemon(t, l)
	b := NewBackend(Options{Layout: l})
	if _, err := b.Connect(); err != nil {
		t.Fatal(err)
	}
	p, err := b.CreateProfile(client.NewProfile{Kind: "claude", Name: "main", Binary: os.Args[0], Mode: "terminal",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(providertest.TUI)}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := b.CreateEmployee(client.NewEmployee{Name: "Atlas", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := b.OpenTerminal(e.ID, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	b.shutdown() // the window closes

	c, err := client.Discover(context.Background(), l)
	if err != nil {
		t.Fatalf("the daemon is gone after the window closed: %v", err)
	}
	got, err := c.Employee(context.Background(), e.ID)
	if err != nil || !got.TerminalRunning {
		t.Fatalf("the employee's terminal must keep running: %+v, %v", got, err)
	}
	if err := b.TerminalInput(key, "x"); err == nil {
		t.Fatal("a closed window must not keep terminals attached")
	}
	_ = d
}

func TestStreamsChatFromDaemon(t *testing.T) {
	l := newLayout(t)
	startFakeDaemon(t, l)
	col := newCollector()
	b := NewBackend(Options{Layout: l, Emit: col.emit})
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
	// The window learns about the stream before it sends.
	col.waitFor(t, "the event stream", func(r recorded) bool { return r.name == EventDaemon })
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
		t.Fatal("the answer never streamed to the window")
	}
	turns, err := b.History(e.ID, 10, "")
	if err != nil || len(turns) != 1 || turns[0].State != "completed" {
		t.Fatalf("history = %+v, %v", turns, err)
	}
}

func TestSettingsDefaultToQuitOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "desktop.json")
	s, err := NewSettingsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Load().KeepInTray {
		t.Fatal("keeping the app in the tray must be off by default")
	}
	if err := s.Save(Settings{KeepInTray: true}); err != nil {
		t.Fatal(err)
	}
	if !s.Load().KeepInTray {
		t.Fatal("the setting was not saved")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Load().KeepInTray {
		t.Fatal("a damaged file must give the defaults")
	}
}
