package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/api"
	"aotus/internal/netaccess"
)

// identityHeaders are headers a proxy or an attacker might use to claim who a
// request comes from. None of them may ever count.
var identityHeaders = []string{
	"Tailscale-User-Login", "Tailscale-User-Name", "X-Tailscale-User-Login", "X-Forwarded-User",
	"X-Remote-User", "Remote-User", "X-Aotus-Login", "X-Aotus-Caller", "X-Aotus-Role", "Forwarded",
}

func claimOwner(r *http.Request) {
	for _, h := range identityHeaders {
		r.Header.Set(h, ownerLogin)
	}
	r.Header.Set("X-Aotus-Role", "owner")
}

func TestIdentityHeadersAreIgnored(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)

	// A guest on the tailnet who claims to be the owner stays a guest.
	code, b := a.asTailnet(t, anaLogin, "laptop", "GET", "/api/v1/me", nil, claimOwner)
	var me struct{ Login, Role, Method string }
	_ = json.Unmarshal(b, &me)
	if code != 200 || me.Login != anaLogin || me.Role != "guest" || me.Method != "tailnet" {
		t.Fatalf("/me for ana claiming the owner = %d %+v; want ana, guest", code, me)
	}
	if code, b := a.asTailnet(t, anaLogin, "laptop", "POST", "/api/v1/access", map[string]any{"login": "eve@example.com", "acknowledged": true}, claimOwner); code != 403 || !strings.Contains(string(b), "owner_only") {
		t.Fatalf("ana claiming the owner added a person: %d %s", code, b)
	}
	// An unlisted person claiming the owner is refused, and audited as themselves.
	if code, _ := a.asTailnet(t, "eve@example.com", "evil", "GET", "/api/v1/employees", nil, claimOwner); code != 403 {
		t.Fatalf("eve claiming the owner = %d, want 403", code)
	}
	rows, err := a.st.Audit(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Action == "refused" && !strings.Contains(r.Caller, "eve@example.com") {
			t.Errorf("a refusal was recorded under a claimed identity: %+v", r)
		}
	}

	// On loopback the token is the identity; headers add nothing to it.
	_, b = a.raw(t, "GET", "/api/v1/me", token, nil, http.Header{"Tailscale-User-Login": {ownerLogin}, "X-Aotus-Login": {anaLogin}})
	me = struct{ Login, Role, Method string }{}
	_ = json.Unmarshal(b, &me)
	if me.Method != "token" || me.Login != "" || me.Role != "owner" {
		t.Fatalf("/me on loopback with identity headers = %+v; want the token, no login", me)
	}
	// And without the token no header opens anything.
	resp, _ := a.raw(t, "GET", "/api/v1/employees", "", nil, http.Header{"Tailscale-User-Login": {ownerLogin}})
	if resp.StatusCode != 401 {
		t.Fatalf("identity header without a token on loopback = %d, want 401", resp.StatusCode)
	}
}

func TestFailedAttemptsAreRateLimited(t *testing.T) {
	a := newAPIConfig(t, func(c *api.Config) { c.MaxFailures = 3; c.FailureWindow = time.Minute })
	a.setUpAccess(t)

	// Loopback: three wrong tokens, then 429 with Retry-After.
	for i := 0; i < 3; i++ {
		if resp, _ := a.raw(t, "GET", "/api/v1/employees", "wrong", nil, nil); resp.StatusCode != 401 {
			t.Fatalf("wrong token #%d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, b := a.raw(t, "GET", "/api/v1/employees", "wrong", nil, nil)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" || !strings.Contains(string(b), "too_many_attempts") {
		t.Fatalf("4th wrong token = %d %q (Retry-After %q); want 429 too_many_attempts", resp.StatusCode, b, resp.Header.Get("Retry-After"))
	}
	// The valid token is never locked out by someone else's failures.
	a.call(t, "GET", "/api/v1/employees", nil, 200, nil)

	// Tailnet: counted per person, not per daemon.
	for i := 0; i < 3; i++ {
		if code, _ := a.asTailnet(t, "eve@example.com", "evil", "GET", "/api/v1/employees", nil, nil); code != 403 {
			t.Fatalf("eve #%d = %d, want 403", i+1, code)
		}
	}
	for i := 0; i < 20; i++ { // a flood after the limit
		if code, _ := a.asTailnet(t, "EVE@example.com", "evil-2", "GET", "/api/v1/employees", nil, nil); code != 429 {
			t.Fatalf("eve after the limit = %d, want 429 (the same login from another device counts too)", code)
		}
	}
	if code, _ := a.asTailnet(t, "mallory@example.com", "m", "GET", "/api/v1/employees", nil, nil); code != 403 {
		t.Fatalf("another unlisted person = %d, want 403 (not blocked by eve's failures)", code)
	}
	if code, _ := a.asTailnet(t, anaLogin, "laptop", "GET", "/api/v1/employees", nil, nil); code != 200 {
		t.Fatalf("a listed person = %d, want 200", code)
	}

	// The flood did not reach the audit log: three refusals and one note.
	rows, err := a.st.Audit(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Caller), "eve@example.com") {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("%d audit rows for eve after 23 requests; want 4 (3 refusals and the note that she hit the limit)", n)
	}

	// The window ends and the caller is heard again.
	short := newAPIConfig(t, func(c *api.Config) { c.MaxFailures = 1; c.FailureWindow = 200 * time.Millisecond })
	short.raw(t, "GET", "/api/v1/employees", "wrong", nil, nil)
	if resp, _ := short.raw(t, "GET", "/api/v1/employees", "wrong", nil, nil); resp.StatusCode != 429 {
		t.Fatalf("over the limit = %d, want 429", resp.StatusCode)
	}
	time.Sleep(250 * time.Millisecond)
	if resp, _ := short.raw(t, "GET", "/api/v1/employees", "wrong", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("after the window = %d, want 401", resp.StatusCode)
	}
}

// serveTailnet serves the API through a real identifying listener, as the
// daemon's tailnet listener does (on loopback, with a fixed identity table).
func serveTailnet(t *testing.T, a *testAPI, who netaccess.StaticIdentifier) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: a.handler, ConnContext: netaccess.ConnContext, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(netaccess.IdentifyListener(ln, who, slog.New(slog.DiscardHandler))) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func tailnetGet(t *testing.T, addr, host, path, tok string) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String(), nil
}

func TestTokenNotAcceptedOnTailnetListener(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)

	// Eve's device is on the tailnet but she is not on the list: the local token
	// she somehow got does not let her in, whatever Host she names.
	eve := serveTailnet(t, a, netaccess.StaticIdentifier{"127.0.0.1": {Login: "eve@example.com", Device: "evil"}})
	for _, host := range []string{tailHost, "127.0.0.1", "localhost"} {
		code, body, err := tailnetGet(t, eve, host, "/api/v1/employees", token)
		if err != nil || code != 403 {
			t.Errorf("token on the tailnet listener, Host %s = %d %s %v; want 403", host, code, body, err)
		}
	}
	// A listed person needs no token there, and the token is not what lets them in.
	ana := serveTailnet(t, a, netaccess.StaticIdentifier{"127.0.0.1": {Login: anaLogin, Device: "laptop"}})
	if code, body, err := tailnetGet(t, ana, tailHost, "/api/v1/me", ""); err != nil || code != 200 || !strings.Contains(body, anaLogin) {
		t.Fatalf("ana without a token = %d %s %v; want 200 as ana", code, body, err)
	}
	if code, _, _ := tailnetGet(t, ana, "127.0.0.1", "/api/v1/me", token); code != 403 {
		t.Fatalf("ana with the token and a loopback Host = %d; want 403 (the loopback rules never apply on the tailnet listener)", code)
	}

	// A peer the tailnet does not vouch for never reaches the handlers.
	before, _ := a.st.Audit(context.Background(), "", 0)
	stranger := serveTailnet(t, a, netaccess.StaticIdentifier{})
	if code, _, err := tailnetGet(t, stranger, tailHost, "/api/v1/employees", token); err == nil {
		t.Fatalf("an unidentified peer got an answer (%d); want the connection closed", code)
	}
	after, _ := a.st.Audit(context.Background(), "", 0)
	if len(after) != len(before) {
		t.Fatalf("an unidentified peer reached the handlers: the audit log grew from %d to %d rows", len(before), len(after))
	}
}

// syncBuffer is a bytes.Buffer safe for the server's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestNoSecretsInLogsWithTailnetEnabled(t *testing.T) {
	var logs syncBuffer
	a := newAPIConfig(t, func(c *api.Config) {
		c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		c.MaxFailures = 2
	})
	a.setUpAccess(t)
	const apiKey = "sk-live-0123456789-very-secret"

	// An API profile with a key, and an employee whose turn fails against a
	// dead endpoint (errors are where secrets usually leak).
	var p struct {
		ID string `json:"id"`
	}
	a.call(t, "POST", "/api/v1/profiles", map[string]any{"kind": "openai-api", "name": "api", "mode": "api", "model": "gpt-x", "base_url": "http://127.0.0.1:1/v1"}, 201, &p)
	a.call(t, "PUT", "/api/v1/profiles/"+p.ID+"/api-key", map[string]string{"key": apiKey}, 204, nil)
	emp := a.employee(t, "Atlas", p.ID)
	var turn struct {
		TurnID string `json:"turn_id"`
	}
	a.call(t, "POST", "/api/v1/employees/"+emp+"/turns", map[string]string{"prompt": "hello"}, 202, &turn)
	deadline := time.Now().Add(15 * time.Second)
	for {
		var hist []struct{ State string }
		a.call(t, "GET", "/api/v1/employees/"+emp+"/history", nil, 200, &hist)
		if len(hist) == 1 && hist[0].State != "running" && hist[0].State != "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn did not end: %+v", hist)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, events := a.raw(t, "GET", "/api/v1/turns/"+turn.TurnID+"/events", token, nil, nil)

	// Remote traffic, refused and served, with secrets in places they must not stick.
	for i := 0; i < 4; i++ {
		a.asTailnet(t, "eve@example.com", "evil", "GET", "/api/v1/employees", nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Api-Key", apiKey)
		})
	}
	a.asTailnet(t, anaLogin, "laptop", "GET", "/api/v1/profiles", nil, nil)
	for i := 0; i < 4; i++ { // wrong tokens on loopback, one of them the API key
		a.raw(t, "GET", "/api/v1/employees", apiKey, nil, nil)
	}

	var audit bytes.Buffer
	rows, err := a.st.Audit(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		audit.WriteString(r.Kind + r.Action + r.Detail + r.Caller + r.Decision + "\n")
	}
	var files bytes.Buffer // the whole data directory: database, WAL, logs, notes
	_ = filepath.WalkDir(a.layout.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(path) == "token" { // the token file is where the token lives
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		files.Write(b)
		return nil
	})
	if !strings.Contains(logs.String(), "too many") {
		t.Fatalf("the logs did not record the limit; the check below would prove little:\n%s", logs.String())
	}
	for name, secret := range map[string]string{"the local token": token, "the API key": apiKey} {
		for where, text := range map[string]string{"the logs": logs.String(), "the audit log": audit.String(), "the turn's events": string(events), "the data directory": files.String()} {
			if strings.Contains(text, secret) {
				t.Errorf("%s is in %s", name, where)
			}
		}
	}
}
