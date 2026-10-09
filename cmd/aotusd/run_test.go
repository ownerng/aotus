package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
)

type daemon struct {
	layout datadir.Layout
	done   chan int
	cancel context.CancelFunc
	out    *bytes.Buffer
}

func startDaemon(t *testing.T, root string) *daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{layout: datadir.Layout{Root: root}, done: make(chan int, 1), cancel: cancel, out: &bytes.Buffer{}}
	go func() { d.done <- run(ctx, []string{"--data-dir", root}, d.out, io.Discard) }()
	t.Cleanup(cancel)
	return d
}

func (d *daemon) waitReady(t *testing.T) lifecycle.Discovery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := lifecycle.ReadDiscovery(d.layout); err == nil {
			return info
		}
		select {
		case code := <-d.done:
			t.Fatalf("the daemon exited early with code %d", code)
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("the daemon never published its address")
	return lifecycle.Discovery{}
}

func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestDaemonLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "aotus")
	d := startDaemon(t, root)
	info := d.waitReady(t)

	if !strings.HasPrefix(info.Address, "127.0.0.1:") || info.PID != os.Getpid() {
		t.Fatalf("discovery = %+v, want a loopback address", info)
	}
	tokenBytes, err := os.ReadFile(d.layout.Token())
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	url := "http://" + info.Address + "/api/v1/status"

	if code, _ := get(t, url, ""); code != http.StatusUnauthorized {
		t.Errorf("without a token: %d, want 401", code)
	}
	if code, _ := get(t, url, "wrong"); code != http.StatusUnauthorized {
		t.Errorf("with a wrong token: %d, want 401", code)
	}
	if code, body := get(t, url, token); code != http.StatusOK || !strings.Contains(body, `"status":"ok"`) || !strings.Contains(body, `"employees":0`) {
		t.Errorf("with the token: %d %s", code, body)
	}

	// A second daemon on the same data directory refuses to start and says so.
	var errOut bytes.Buffer
	if code := run(context.Background(), []string{"--data-dir", root}, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), "already running") {
		t.Errorf("second daemon: code %d, %q; want 1 and an 'already running' message", code, errOut.String())
	}

	// A clean shutdown withdraws the address and frees the lock.
	d.cancel()
	select {
	case code := <-d.done:
		if code != 0 {
			t.Fatalf("exit code %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not stop")
	}
	if _, err := lifecycle.ReadDiscovery(d.layout); err == nil {
		t.Error("the discovery file must be removed on shutdown")
	}
	if !strings.Contains(d.out.String(), "stopped") {
		t.Errorf("output = %q", d.out.String())
	}
	again := startDaemon(t, root)
	again.waitReady(t)
	again.cancel()
	<-again.done
}

func TestRefusesPublicAddress(t *testing.T) {
	var errOut bytes.Buffer
	code := run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "d"), "--addr", "0.0.0.0:8080"}, io.Discard, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "loopback") {
		t.Fatalf("code %d, %q; want 1 and a message about loopback", code, errOut.String())
	}
}

func TestVersionAndAutostartFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "aotusd ") {
		t.Fatalf("--version: %d %q", code, out.String())
	}
	if code := run(context.Background(), []string{"--autostart", "maybe", "--data-dir", t.TempDir()}, &out, &errOut); code != 2 {
		t.Fatalf("a bad --autostart value: %d, want 2", code)
	}
	// Use a scratch home so the test never touches the real login items.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("USERPROFILE", home)
	if _, err := os.UserHomeDir(); err != nil || os.Getenv("HOME") != home {
		t.Skip("cannot redirect the home directory here")
	}
	if runtime.GOOS == "windows" {
		t.Skip("autostart on Windows uses the registry; covered by the lifecycle tests")
	}
	for _, step := range []struct{ arg, want string }{{"status", "off"}, {"on", ""}, {"status", "on"}, {"off", ""}, {"status", "off"}} {
		out.Reset()
		if code := run(context.Background(), []string{"--autostart", step.arg, "--data-dir", t.TempDir()}, &out, &errOut); code != 0 {
			t.Fatalf("--autostart %s: code %d, %s", step.arg, code, errOut.String())
		}
		if step.want != "" && !strings.Contains(out.String(), step.want) {
			t.Fatalf("--autostart %s printed %q, want %q", step.arg, out.String(), step.want)
		}
	}
}
