package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aotus/internal/api"
	"aotus/internal/datadir"
	"aotus/internal/netaccess"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// startRemote serves the API on a listener that identifies every peer as who,
// the way the tailnet listener does, and returns its address.
func startRemote(t *testing.T, who netaccess.Caller, owner string) string {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.SetOwnerLogin(context.Background(), owner)
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{provider.KindClaude: provider.Claude{}}, orchestrator.Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	h := api.New(api.Config{Manager: mgr, Broker: permissions.New(st), Token: "unused", Version: "test", Store: st, TailnetHosts: []string{"127.0.0.1"}})
	var lc net.ListenConfig
	raw, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ConnContext: netaccess.ConnContext, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = srv.Serve(netaccess.IdentifyListener(raw, netaccess.StaticIdentifier{"127.0.0.1": who}, nil))
	}()
	t.Cleanup(func() { _ = srv.Close() })
	return raw.Addr().String()
}

// cliWith runs the command with a connections file of its own.
func (r *rig) cliWith(t *testing.T, file string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	code := run(ctx, append([]string{"--data-dir", r.layout.Root, "--connections-file", file}, args...), strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestConnectionsCommands(t *testing.T) {
	r := startDaemon(t)
	addr := startRemote(t, netaccess.Caller{Login: "owner@example.com", Device: "laptop"}, "owner@example.com")
	file := filepath.Join(t.TempDir(), "connections.json")

	if code, out, _ := r.cliWith(t, file, "connections"); code != 0 || !strings.Contains(out, "local") || !strings.Contains(out, "this computer") {
		t.Fatalf("empty list: %d %q", code, out)
	}
	if code, _, e := r.cliWith(t, file, "connection", "add", "vps", "https://token@"+addr); code != 1 || !strings.Contains(e, "host:port") {
		t.Fatalf("a URL with a secret must be refused: %d %q", code, e)
	}
	if code, _, e := r.cliWith(t, file, "connection", "add", "vps"); code != 2 {
		t.Fatalf("missing address: %d %q", code, e)
	}
	if code, out, e := r.cliWith(t, file, "connection", "add", "vps", addr); code != 0 || !strings.Contains(out, "saved vps") {
		t.Fatalf("add: %d %q %q", code, out, e)
	}
	if code, out, _ := r.cliWith(t, file, "connections"); code != 0 || !strings.Contains(out, "vps") || !strings.Contains(out, addr) {
		t.Fatalf("list: %d %q", code, out)
	}

	// test dials it and says who we are there.
	code, out, e := r.cliWith(t, file, "connection", "test", "vps")
	if code != 0 || !strings.Contains(out, "owner@example.com") || !strings.Contains(out, "role owner") || !strings.Contains(out, "on vps") {
		t.Fatalf("test: %d %q %q", code, out, e)
	}
	// Any command can run against it.
	if code, out, e := r.cliWith(t, file, "--connection", "vps", "whoami"); code != 0 || !strings.Contains(out, "owner@example.com") {
		t.Fatalf("--connection: %d %q %q", code, out, e)
	}
	if code, _, e := r.cliWith(t, file, "--connection", "nope", "status"); code != 1 || !strings.Contains(e, "no saved connection") {
		t.Fatalf("unknown connection: %d %q", code, e)
	}

	if code, _, _ := r.cliWith(t, file, "connection", "use", "vps"); code != 0 {
		t.Fatal("use")
	}
	if _, out, _ := r.cliWith(t, file, "connections"); !strings.Contains(out, "(desktop opens this)") {
		t.Fatalf("the active one must be marked: %q", out)
	}
	if code, _, _ := r.cliWith(t, file, "connection", "use", "nope"); code != 1 {
		t.Fatal("using an unknown connection must fail")
	}
	if code, _, _ := r.cliWith(t, file, "connection", "rm", "vps"); code != 0 {
		t.Fatal("rm")
	}
	if code, _, _ := r.cliWith(t, file, "connection", "rm", "vps"); code != 1 {
		t.Fatal("removing it twice must fail")
	}

	// A server that does not allow us says what to do.
	stranger := startRemote(t, netaccess.Caller{Login: "eve@example.com", Device: "x"}, "owner@example.com")
	_, _, _ = r.cliWith(t, file, "connection", "add", "closed", stranger)
	if code, _, e := r.cliWith(t, file, "connection", "test", "closed"); code != 1 || !strings.Contains(e, "allow-list") || !strings.Contains(e, "aotus access allow") {
		t.Fatalf("not allowed: %d %q", code, e)
	}
}

func TestAccessCommands(t *testing.T) {
	r := startDaemon(t)
	if err := r.st.SetOwnerLogin(context.Background(), "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	p := r.profile(t, "main", providertest.Hello)

	// Without --acknowledge the notice is shown and nothing is stored.
	code, out, e := r.cli(t, "", "access", "allow", "ana@example.com")
	if code != 2 || !strings.Contains(out, "ana@example.com") || !strings.Contains(out, "terms") || !strings.Contains(e, "--acknowledge") {
		t.Fatalf("allow without the acknowledgement: %d out=%q err=%q", code, out, e)
	}
	if list, _ := r.st.AccessList(context.Background()); len(list) != 0 {
		t.Fatalf("nothing may be stored: %+v", list)
	}
	if code, out, e := r.cli(t, "", "access", "allow", "ana@example.com", "--acknowledge"); code != 0 || !strings.Contains(out, "may now call") {
		t.Fatalf("allow: %d %q %q", code, out, e)
	}
	if code, _, _ := r.cli(t, "", "access", "share", p.ID, "ana@example.com"); code != 0 {
		t.Fatal("share")
	}
	code, out, _ = r.cli(t, "", "access", "list")
	if code != 0 || !strings.Contains(out, "owner: owner@example.com") || !strings.Contains(out, "ana@example.com") || !strings.Contains(out, p.ID) {
		t.Fatalf("list: %d %q", code, out)
	}
	if code, _, _ := r.cli(t, "", "access", "unshare", p.ID, "ana@example.com"); code != 0 {
		t.Fatal("unshare")
	}
	if code, _, _ := r.cli(t, "", "access", "deny", "ana@example.com"); code != 0 {
		t.Fatal("deny")
	}
	if code, out, _ := r.cli(t, "", "access", "list"); code != 0 || strings.Contains(out, "ana@example.com") {
		t.Fatalf("after deny: %q", out)
	}
	if code, _, _ := r.cli(t, "", "access"); code != 2 {
		t.Fatalf("access without a subcommand must be a usage error, got %d", code)
	}
	// On loopback the token makes us the owner.
	if code, out, _ := r.cli(t, "", "whoami"); code != 0 || !strings.Contains(out, "local owner") {
		t.Fatalf("whoami: %d %q", code, out)
	}
}
