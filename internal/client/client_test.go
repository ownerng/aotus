package client_test

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

const token = "client-test-token-0123456789abcdef0123456789abcdef0123456789abcd"

func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}

type daemon struct {
	layout datadir.Layout
	srv    *httptest.Server
	broker *permissions.Broker
}

// startDaemon runs the real API over real storage and publishes it the way
// aotusd does: a discovery file and a token file in the data directory.
func startDaemon(t *testing.T) *daemon {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{
		provider.KindClaude: provider.Claude{},
	}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	broker := permissions.New(st)
	srv := httptest.NewServer(api.New(api.Config{Manager: mgr, Broker: broker, Token: token, Version: "test"}))
	t.Cleanup(srv.Close)
	if err := os.WriteFile(l.Token(), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.WriteDiscovery(l, lifecycle.Discovery{PID: os.Getpid(), Address: strings.TrimPrefix(srv.URL, "http://"), StartedAt: time.Now(), Version: "test"}); err != nil {
		t.Fatal(err)
	}
	return &daemon{layout: l, srv: srv, broker: broker}
}

func (d *daemon) client(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.Discover(context.Background(), d.layout)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeProfile(t *testing.T, c *client.Client, name string, sc providertest.Scenario, mode string) client.Profile {
	t.Helper()
	p, err := c.CreateProfile(context.Background(), client.NewProfile{
		Kind: "claude", Name: name, Binary: os.Args[0], Mode: mode,
		ExtraEnv: map[string]string{
			providertest.EnvFakeCLI:      "1",
			providertest.EnvFakeScenario: string(sc),
			providertest.EnvFakePidFile:  filepath.Join(t.TempDir(), "pids"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode == "structured" {
		if err := c.AcceptNotice(context.Background(), p.ID, "claude-headless"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestClientDiscoversDaemon(t *testing.T) {
	ctx := context.Background()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discover(ctx, l); !errors.Is(err, client.ErrNoDaemon) {
		t.Fatalf("with no daemon: %v, want ErrNoDaemon", err)
	}

	// A discovery file left behind by a daemon that died: nothing answers there.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()
	_ = os.WriteFile(l.Token(), []byte(token+"\n"), 0o600)
	_ = lifecycle.WriteDiscovery(l, lifecycle.Discovery{PID: 1, Address: dead, StartedAt: time.Now()})
	if _, err := client.Discover(ctx, l); !errors.Is(err, client.ErrNoDaemon) || !strings.Contains(err.Error(), dead) {
		t.Fatalf("with a stale discovery file: %v, want ErrNoDaemon naming the dead address", err)
	}

	d := startDaemon(t)
	c, err := client.Discover(ctx, d.layout)
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(ctx)
	if err != nil || st.Status != "ok" || st.Version != "test" || st.Employees != 0 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if c.Address() != strings.TrimPrefix(d.srv.URL, "http://") {
		t.Fatalf("address = %s", c.Address())
	}

	// A wrong token is refused, and the client says so rather than "no daemon".
	_ = os.WriteFile(d.layout.Token(), []byte("not-the-token\n"), 0o600)
	if _, err := client.Discover(ctx, d.layout); !client.HasCode(err, "unauthorized") || errors.Is(err, client.ErrNoDaemon) {
		t.Fatalf("with a wrong token: %v, want an unauthorized API error", err)
	}
}

func TestClientListsEmployees(t *testing.T) {
	ctx := context.Background()
	c := startDaemon(t).client(t)
	p := fakeProfile(t, c, "main", providertest.Hello, "structured")

	if got, _ := c.Employees(ctx); len(got) != 0 {
		t.Fatalf("a new daemon has employees: %+v", got)
	}
	a, err := c.CreateEmployee(ctx, client.NewEmployee{Name: "Atlas", Role: "reviewer", SystemPrompt: "Be brief", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateEmployee(ctx, client.NewEmployee{Name: "Bruno", ProfileID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateEmployee(ctx, client.NewEmployee{Name: "atlas", ProfileID: p.ID}); !client.HasCode(err, "name_taken") {
		t.Fatalf("a duplicate name: %v, want name_taken", err)
	}
	list, err := c.Employees(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Atlas" || list[0].Role != "reviewer" || list[0].State != "active" {
		t.Fatalf("employees = %+v, %v", list, err)
	}
	profiles, _ := c.Profiles(ctx)
	if len(profiles) != 1 || profiles[0].ID != p.ID || len(profiles[0].AcceptedNotices) != 1 {
		t.Fatalf("profiles = %+v", profiles)
	}
	det, err := c.Detect(ctx, p.ID)
	if err != nil || !det.Installed || det.Version != "2.1.295" {
		t.Fatalf("detect = %+v, %v", det, err)
	}

	if err := c.Pause(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := c.Employee(ctx, a.ID); e.State != "paused" {
		t.Fatalf("state = %s", e.State)
	}
	if _, err := c.Send(ctx, a.ID, "hi"); !client.HasCode(err, "paused") {
		t.Fatalf("a paused employee takes no turns: %v", err)
	}
	if err := c.Resume(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteEmployee(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Employee(ctx, a.ID); !client.HasCode(err, "not_found") {
		t.Fatalf("a deleted employee: %v", err)
	}
	if err := c.DeleteProfile(ctx, p.ID); !client.HasCode(err, "profile_in_use") {
		t.Fatalf("a profile in use: %v", err)
	}

	// Memory through the client.
	b := list[1]
	id, err := c.AddFact(ctx, b.ID, "k", "Release day is Friday")
	if err != nil {
		t.Fatal(err)
	}
	if facts, _ := c.Facts(ctx, b.ID); len(facts) != 1 || facts[0].ID != id {
		t.Fatalf("facts = %+v", facts)
	}
	if hits, _ := c.SearchMemory(ctx, b.ID, "friday release", 5); len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if err := c.DeleteFact(ctx, b.ID, id); err != nil {
		t.Fatal(err)
	}
}

func TestClientStreamsTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := startDaemon(t).client(t)
	p := fakeProfile(t, c, "main", providertest.Hello, "structured")
	e, err := c.CreateEmployee(ctx, client.NewEmployee{Name: "Atlas", ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := c.Events(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if m, err := stream.Next(ctx); err != nil || m.Type != "hello" {
		t.Fatalf("first message = %+v, %v", m, err)
	}
	turnID, err := c.Send(ctx, e.ID, "ping")
	if err != nil || turnID == "" {
		t.Fatalf("Send = %q, %v", turnID, err)
	}
	var text string
	var kinds []string
	for {
		m, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.Type != "update" {
			continue
		}
		kinds = append(kinds, m.Update.Kind)
		if m.Update.Event != nil && m.Update.Event.Kind == "text" {
			text += m.Update.Event.Text
		}
		if m.Update.Kind == "turn_ended" {
			if m.Update.State != "completed" {
				t.Fatalf("turn ended as %s", m.Update.State)
			}
			break
		}
	}
	if text != "pong" || kinds[0] != "turn_queued" {
		t.Fatalf("text %q, kinds %v", text, kinds)
	}
	hist, err := c.History(ctx, e.ID, 10, "")
	if err != nil || len(hist) != 1 || hist[0].ID != turnID || hist[0].State != "completed" {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if older, _ := c.History(ctx, e.ID, 10, hist[0].StartedAt); len(older) != 0 {
		t.Fatalf("nothing is older than the first turn: %+v", older)
	}
	events, err := c.TurnEvents(ctx, turnID)
	if err != nil || events[len(events)-1].Kind != "done" || events[len(events)-1].Reason != "completed" {
		t.Fatalf("events = %+v, %v", events, err)
	}

	// Cancel a turn that does not end by itself.
	sleepProfile := fakeProfile(t, c, "sleepy", providertest.Sleep, "structured")
	s, _ := c.CreateEmployee(ctx, client.NewEmployee{Name: "Sleepy", ProfileID: sleepProfile.ID})
	all, _ := c.Events(ctx, s.ID)
	defer func() { _ = all.Close() }()
	if _, err := c.Send(ctx, s.ID, "wait"); err != nil {
		t.Fatal(err)
	}
	for {
		m, err := all.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.Type == "update" && m.Update.Kind == "turn_started" {
			break
		}
	}
	if err := c.Cancel(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	for {
		m, err := all.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if m.Type == "update" && m.Update.Kind == "turn_ended" {
			if m.Update.State != "canceled" {
				t.Fatalf("canceled turn ended as %s", m.Update.State)
			}
			break
		}
	}
	if err := c.Cancel(ctx, s.ID); !client.HasCode(err, "no_turn") {
		t.Fatalf("canceling nothing: %v", err)
	}
}

func TestClientAnswersApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d := startDaemon(t)
	c := d.client(t)
	stream, err := c.Events(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	decision := make(chan string, 2)
	check := func() {
		dec, why, _ := d.broker.Check(ctx, permissions.Action{EmployeeID: "emp-1", Kind: permissions.Network, Target: "example.com"})
		decision <- string(dec) + "/" + string(why)
	}
	go check()
	var req *client.Approval
	for req == nil {
		m, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		req = m.Approval
	}
	if req.EmployeeID != "emp-1" || req.Kind != "network" || req.Target != "example.com" {
		t.Fatalf("approval = %+v", req)
	}
	if pending, _ := c.Approvals(ctx); len(pending) != 1 || pending[0].ID != req.ID {
		t.Fatalf("pending = %+v", pending)
	}
	if err := c.Answer(ctx, req.ID, true, "kind"); err != nil {
		t.Fatal(err)
	}
	if got := <-decision; got != "allowed/user" {
		t.Fatalf("decision = %s", got)
	}
	if err := c.Answer(ctx, req.ID, true, "none"); !client.HasCode(err, "request_not_found") {
		t.Fatalf("answering twice: %v", err)
	}
	go check()
	if got := <-decision; got != "allowed/remembered" {
		t.Fatalf("second decision = %s", got)
	}
	grants, _ := c.Grants(ctx, "emp-1")
	if len(grants) != 1 || grants[0].Kind != "network" || grants[0].Target != "*" || !grants[0].Allow {
		t.Fatalf("grants = %+v", grants)
	}
	if err := c.SetGrant(ctx, "emp-1", client.Grant{Kind: "network", Target: "evil.example", Allow: false}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGrant(ctx, "emp-1", "network", "*"); err != nil {
		t.Fatal(err)
	}
	if grants, _ := c.Grants(ctx, "emp-1"); len(grants) != 1 || grants[0].Target != "evil.example" {
		t.Fatalf("grants after editing = %+v", grants)
	}
}
