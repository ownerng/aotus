package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"aotus/internal/api"
	"aotus/internal/datadir"
	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

const token = "test-token-0123456789abcdef0123456789abcdef0123456789abcdef01234567"

// memKeys is an in-memory credential store.
type memKeys struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKeys) Set(ref, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[ref] = v
	return nil
}

func (k *memKeys) Delete(ref string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, ref)
	return nil
}

func (k *memKeys) Get(ref string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[ref], nil
}

type testAPI struct {
	*httptest.Server
	handler *api.Server
	layout  datadir.Layout
	mgr     *orchestrator.Manager
	broker  *permissions.Broker
	st      *store.Store
	keys    *memKeys
}

func newAPI(t *testing.T, allowed ...string) *testAPI {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys := &memKeys{}
	mgr := orchestrator.NewManager(orchestrator.New(st, workspace.New(l)), map[provider.Kind]provider.Provider{
		provider.KindClaude: provider.Claude{},
		provider.KindCodex:  provider.Codex{},
		provider.KindOpenAI: provider.OpenAICompat{Keys: keys.Get},
	}, orchestrator.Options{Keys: keys})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})
	broker := permissions.New(st)
	h := api.New(api.Config{Manager: mgr, Broker: broker, Token: token, Version: "test", AllowedOrigins: allowed, Store: st, TailnetHosts: []string{"vps.tail1234.ts.net", "100.64.0.7"}})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testAPI{Server: srv, handler: h, layout: l, mgr: mgr, broker: broker, st: st, keys: keys}
}

// call sends a request with the token and decodes a JSON answer into out.
func (a *testAPI) call(t *testing.T, method, path string, body any, want int, out any) {
	t.Helper()
	resp, b := a.raw(t, method, path, token, body, nil)
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: bad JSON %q: %v", method, path, b, err)
		}
	}
}

func (a *testAPI) raw(t *testing.T, method, path, tok string, body any, header http.Header) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	switch v := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(v)
	default:
		b, _ := json.Marshal(v)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, a.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, vs := range header {
		for _, v := range vs {
			if strings.EqualFold(k, "Host") {
				req.Host = v
			} else {
				req.Header.Add(k, v)
			}
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (a *testAPI) dial(t *testing.T, path string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if header == nil {
		header = http.Header{}
	}
	if header.Get("Authorization") == "" {
		header.Set("Authorization", "Bearer "+token)
	}
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(a.URL, "http")+path, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

// message mirrors the events stream frame.
type message struct {
	Type   string `json:"type"`
	Update *struct {
		Seq        uint64 `json:"seq"`
		Kind       string `json:"kind"`
		EmployeeID string `json:"employee_id"`
		TurnID     string `json:"turn_id"`
		State      string `json:"state"`
		Event      *struct {
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Reason string `json:"reason"`
		} `json:"event"`
	} `json:"update"`
	Approval *struct {
		ID         string `json:"id"`
		EmployeeID string `json:"employee_id"`
		Kind       string `json:"kind"`
		Target     string `json:"target"`
	} `json:"approval"`
}

func readMessage(t *testing.T, c *websocket.Conn) message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("reading a message: %v", err)
	}
	var m message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("bad message %q: %v", b, err)
	}
	return m
}

// readUntil reads messages until one matches.
func readUntil(t *testing.T, c *websocket.Conn, match func(message) bool) message {
	t.Helper()
	for {
		if m := readMessage(t, c); match(m) {
			return m
		}
	}
}

// fakeProfile creates a Claude Code profile whose CLI is the fake one, through
// the API itself, and returns its ID.
func (a *testAPI) fakeProfile(t *testing.T, name string, sc providertest.Scenario, mode string) string {
	t.Helper()
	var p struct {
		ID string `json:"id"`
	}
	a.call(t, "POST", "/api/v1/profiles", map[string]any{
		"kind": "claude", "name": name, "binary": os.Args[0], "mode": mode,
		"extra_env": map[string]string{
			providertest.EnvFakeCLI:      "1",
			providertest.EnvFakeScenario: string(sc),
			providertest.EnvFakePidFile:  filepath.Join(t.TempDir(), "pids"),
		},
	}, 201, &p)
	if mode == "structured" {
		a.call(t, "POST", "/api/v1/profiles/"+p.ID+"/notices", map[string]string{"notice": "claude-headless"}, 204, nil)
	}
	return p.ID
}

func (a *testAPI) employee(t *testing.T, name, profile string) string {
	t.Helper()
	var e struct {
		ID string `json:"id"`
	}
	a.call(t, "POST", "/api/v1/employees", map[string]any{"name": name, "role": "tester", "profile_id": profile}, 201, &e)
	return e.ID
}

func actionFor(employee, target string) permissions.Action {
	return permissions.Action{EmployeeID: employee, Kind: permissions.RunCommand, Target: target}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
