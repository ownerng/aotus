package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

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

const token = "cli-test-token-0123456789abcdef0123456789abcdef0123456789abcdef0123"

func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}

type rig struct {
	st     *store.Store
	layout datadir.Layout
	broker *permissions.Broker
	c      *client.Client
}

func startDaemon(t *testing.T) *rig {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	broker := permissions.New(st)
	srv := httptest.NewServer(api.New(api.Config{Manager: mgr, Broker: broker, Token: token, Version: "test", Store: st}))
	t.Cleanup(srv.Close)
	_ = os.WriteFile(l.Token(), []byte(token+"\n"), 0o600)
	_ = lifecycle.WriteDiscovery(l, lifecycle.Discovery{PID: os.Getpid(), Address: strings.TrimPrefix(srv.URL, "http://"), StartedAt: time.Now(), Version: "test"})
	c, err := client.Discover(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{layout: l, broker: broker, c: c, st: st}
}

// cli runs the command and returns its exit code and output.
func (r *rig) cli(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	code := run(ctx, append([]string{"--data-dir", r.layout.Root}, args...), strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func (r *rig) profile(t *testing.T, name string, sc providertest.Scenario) client.Profile {
	t.Helper()
	p, err := r.c.CreateProfile(context.Background(), client.NewProfile{
		Kind: "claude", Name: name, Binary: os.Args[0], Mode: "structured",
		ExtraEnv: map[string]string{providertest.EnvFakeCLI: "1", providertest.EnvFakeScenario: string(sc), providertest.EnvFakePidFile: filepath.Join(t.TempDir(), "p")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.c.AcceptNotice(context.Background(), p.ID, "claude-headless"); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatusCommandPrintsDaemonState(t *testing.T) {
	r := startDaemon(t)
	code, out, errOut := r.cli(t, "", "status")
	if code != 0 || !strings.Contains(out, "daemon test at 127.0.0.1:") || !strings.Contains(out, "ok, 0 employee(s)") {
		t.Fatalf("status: code %d, out %q, err %q", code, out, errOut)
	}

	// With no daemon the message says what to do.
	empty := t.TempDir()
	var o, e bytes.Buffer
	if code := run(context.Background(), []string{"--data-dir", empty, "status"}, strings.NewReader(""), &o, &e); code != 1 || !strings.Contains(e.String(), "not running") || !strings.Contains(e.String(), "aotusd") {
		t.Fatalf("no daemon: code %d, %q", code, e.String())
	}
	o.Reset()
	if code := run(context.Background(), []string{"--version"}, strings.NewReader(""), &o, &e); code != 0 || !strings.HasPrefix(o.String(), "aotus ") {
		t.Fatalf("--version: %d %q", code, o.String())
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	r := startDaemon(t)
	for _, args := range [][]string{{"nonsense"}, {"profile"}, {"profile", "add"}, {"employee", "add"}, {"chat", "Atlas"}, {"history"}, {"approve"}, {"terminal", "start"}, {"profile", "detect"}} {
		if code, _, errOut := r.cli(t, "", args...); code != 2 || !strings.Contains(errOut, "aotus:") {
			t.Errorf("aotus %v: code %d, %q; want a usage error (2)", args, code, errOut)
		}
	}
	if code, out, _ := r.cli(t, "", "help"); code != 0 || !strings.Contains(out, "chat EMPLOYEE PROMPT") {
		t.Errorf("help: %d %q", code, out)
	}
}

func TestEmployeeAndProfileCommands(t *testing.T) {
	r := startDaemon(t)
	p := r.profile(t, "main", providertest.Hello)

	_, out, _ := r.cli(t, "", "profiles")
	if !strings.Contains(out, p.ID) || !strings.Contains(out, "structured") {
		t.Fatalf("profiles:\n%s", out)
	}
	if code, out, errOut := r.cli(t, "", "profile", "detect", p.ID); code != 0 || !strings.Contains(out, "installed: true") || !strings.Contains(out, "2.1.295") {
		t.Fatalf("detect: %d\n%s\n%s", code, out, errOut)
	}

	code, out, errOut := r.cli(t, "", "employee", "add", "--name", "Atlas", "--role", "reviewer", "--profile", p.ID)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "emp-") {
		t.Fatalf("employee add: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := r.cli(t, "", "employee", "add", "--name", "atlas", "--profile", p.ID); code != 1 || !strings.Contains(errOut, "name_taken") {
		t.Fatalf("duplicate: %d %q", code, errOut)
	}
	if _, out, _ := r.cli(t, "", "employees"); !strings.Contains(out, "Atlas") || !strings.Contains(out, "reviewer") || !strings.Contains(out, "active") {
		t.Fatalf("employees:\n%s", out)
	}
	// By name, case-insensitively.
	if code, _, errOut := r.cli(t, "", "employee", "pause", "ATLAS"); code != 0 {
		t.Fatalf("pause: %q", errOut)
	}
	if _, out, _ := r.cli(t, "", "employees"); !strings.Contains(out, "paused") {
		t.Fatalf("employees after pause:\n%s", out)
	}
	if code, _, errOut := r.cli(t, "", "employee", "resume", "Atlas"); code != 0 {
		t.Fatalf("resume: %q", errOut)
	}
	if code, _, errOut := r.cli(t, "", "employee", "pause", "Nobody"); code != 1 || !strings.Contains(errOut, `no employee called "Nobody"`) {
		t.Fatalf("unknown employee: %d %q", code, errOut)
	}
	if code, _, _ := r.cli(t, "", "employee", "rm", "Atlas"); code != 0 {
		t.Fatal("rm failed")
	}
	if _, out, _ := r.cli(t, "", "employees"); strings.Contains(out, "Atlas") {
		t.Fatalf("a removed employee is still listed:\n%s", out)
	}
	// No credential store in this daemon: the key command reports it clearly.
	if code, _, errOut := r.cli(t, "sk-secret\n", "profile", "key", p.ID); code != 1 || strings.Contains(errOut, "sk-secret") {
		t.Fatalf("key without a credential store: %d %q (the key must never be echoed)", code, errOut)
	}
	if code, _, _ := r.cli(t, "", "profile", "rm", p.ID); code != 0 {
		t.Fatal("profile rm failed")
	}
}

func TestChatStreamsAnAnswer(t *testing.T) {
	r := startDaemon(t)
	ok := r.profile(t, "ok", providertest.Hello)
	bad := r.profile(t, "bad", providertest.Fail)
	for name, p := range map[string]client.Profile{"Atlas": ok, "Broken": bad} {
		if _, err := r.c.CreateEmployee(context.Background(), client.NewEmployee{Name: name, ProfileID: p.ID}); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errOut := r.cli(t, "", "chat", "Atlas", "say", "pong", "please")
	if code != 0 || out != "pong\n" {
		t.Fatalf("chat: code %d, out %q, err %q; want the streamed answer and a newline", code, out, errOut)
	}
	// A failing employee: nonzero exit, the problem on stderr, nothing on stdout.
	code, out, errOut = r.cli(t, "", "chat", "Broken", "hello")
	if code != 1 || out != "" || !strings.Contains(errOut, "/login") {
		t.Fatalf("failing chat: code %d, out %q, err %q", code, out, errOut)
	}
	if _, out, _ := r.cli(t, "", "history", "Atlas"); !strings.Contains(out, "completed") || !strings.Contains(out, "say pong please") {
		t.Fatalf("history:\n%s", out)
	}
	if code, _, errOut := r.cli(t, "", "chat", "Atlas", "x", "--approve", "maybe"); code != 2 {
		t.Fatalf("a bad --approve value: %d %q", code, errOut)
	}
}

func TestApprovalCommands(t *testing.T) {
	r := startDaemon(t)
	if _, out, _ := r.cli(t, "", "approvals"); !strings.Contains(out, "nothing is waiting") {
		t.Fatalf("approvals: %q", out)
	}
	decision := make(chan string, 1)
	go func() {
		d, why, _ := r.broker.Check(context.Background(), permissions.Action{EmployeeID: "emp-9", Kind: permissions.RunCommand, Target: "make test"})
		decision <- string(d) + "/" + string(why)
	}()
	var id string
	deadline := time.Now().Add(10 * time.Second)
	for id == "" {
		if time.Now().After(deadline) {
			t.Fatal("no request appeared")
		}
		if list, _ := r.c.Approvals(context.Background()); len(list) == 1 {
			id = list[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, out, _ := r.cli(t, "", "approvals"); !strings.Contains(out, "make test") || !strings.Contains(out, "run_command") || !strings.Contains(out, id) {
		t.Fatalf("approvals:\n%s", out)
	}
	if code, _, errOut := r.cli(t, "", "approve", id, "--remember", "exact"); code != 0 {
		t.Fatalf("approve: %q", errOut)
	}
	if got := <-decision; got != "allowed/user" {
		t.Fatalf("decision = %s", got)
	}
	if code, _, errOut := r.cli(t, "", "deny", id); code != 1 || !strings.Contains(errOut, "request_not_found") {
		t.Fatalf("answering twice: %d %q", code, errOut)
	}
}
