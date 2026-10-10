package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"aotus/internal/provider/providertest"
)

func TestRequiresToken(t *testing.T) {
	a := newAPI(t)
	routes := a.handler.Routes()
	if len(routes) < 30 {
		t.Fatalf("only %d routes registered: the check below would prove little", len(routes))
	}
	attempts := map[string]func(*http.Request){
		"no token":           func(r *http.Request) {},
		"a wrong token":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") },
		"a different scheme": func(r *http.Request) { r.Header.Set("Authorization", "Token "+token) },
		"the token as plain": func(r *http.Request) { r.Header.Set("Authorization", token) },
		"the token in the URL": func(r *http.Request) {
			q := r.URL.Query()
			q.Set("token", token)
			r.URL.RawQuery = q.Encode()
		},
		"an empty bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") },
	}
	for _, pattern := range routes {
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.NewReplacer("{id}", "x", "{fact}", "1", "{login}", "ana@example.com").Replace(path)
		for name, tweak := range attempts {
			req, _ := http.NewRequestWithContext(context.Background(), method, a.URL+path, nil)
			tweak(req)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s with %s answered %d, want 401", pattern, name, resp.StatusCode)
			}
		}
		// And with the token the guard lets it through to the handler.
		req, _ := http.NewRequestWithContext(context.Background(), method, a.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			t.Errorf("%s with the right token answered %d", pattern, resp.StatusCode)
		}
	}

	var body struct {
		Error struct{ Code, Message string }
	}
	resp, b := a.raw(t, "GET", "/api/v1/status", "", nil, nil)
	if resp.StatusCode != 401 || !strings.Contains(string(b), `"unauthorized"`) || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("the 401 must be a JSON error asking for a bearer token: %d %s %v", resp.StatusCode, b, resp.Header)
	}
	_ = body
	a.call(t, "GET", "/api/v1/status", nil, 200, &struct{ Status string }{})
}

func TestRejectsForeignWebSocketOrigin(t *testing.T) {
	a := newAPI(t)

	// A program connects with no Origin: fine.
	c, _, err := a.dial(t, "/api/v1/events", nil)
	if err != nil {
		t.Fatalf("a client without an Origin must connect: %v", err)
	}
	if m := readMessage(t, c); m.Type != "hello" {
		t.Fatalf("first message = %+v", m)
	}

	// A web page cannot, whatever origin it claims, even with the token.
	for _, origin := range []string{"http://evil.example", "https://evil.example:8443", "null", a.URL, "http://localhost"} {
		_, resp, err := a.dial(t, "/api/v1/events", http.Header{"Origin": {origin}})
		if err == nil {
			t.Errorf("a WebSocket from the origin %q was accepted", origin)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q: %v / %v, want a 403 before the upgrade", origin, resp, err)
		}
	}
	// The same for plain requests: a page cannot call the API.
	resp, _ := a.raw(t, "GET", "/api/v1/status", token, nil, http.Header{"Origin": {"http://evil.example"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-origin GET answered %d, want 403", resp.StatusCode)
	}
	resp, _ = a.raw(t, "POST", "/api/v1/employees", token, map[string]string{"name": "x"}, http.Header{"Origin": {"http://evil.example"}, "Content-Type": {"text/plain"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-origin simple POST answered %d, want 403", resp.StatusCode)
	}

	// DNS rebinding: a name that resolves to 127.0.0.1 but is not ours.
	resp, b := a.raw(t, "GET", "/api/v1/status", token, nil, http.Header{"Host": {"evil.example:80"}})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "bad_host") {
		t.Errorf("a foreign Host header answered %d %s, want 403 bad_host", resp.StatusCode, b)
	}
	for _, host := range []string{"localhost:9", "127.0.0.1:9", "[::1]:9", "localhost"} {
		resp, _ := a.raw(t, "GET", "/api/v1/status", token, nil, http.Header{"Host": {host}})
		if resp.StatusCode != 200 {
			t.Errorf("Host %q answered %d, want 200", host, resp.StatusCode)
		}
	}

	// An origin the operator allow-listed is accepted; others still are not.
	b2 := newAPI(t, "http://app.local")
	if _, _, err := b2.dial(t, "/api/v1/events", http.Header{"Origin": {"http://app.local"}}); err != nil {
		t.Errorf("an allow-listed origin must be accepted: %v", err)
	}
	if _, _, err := b2.dial(t, "/api/v1/events", http.Header{"Origin": {"http://other.local"}}); err == nil {
		t.Error("an origin that is not allow-listed was accepted")
	}
}

func TestNoPermissiveCORS(t *testing.T) {
	a := newAPI(t)
	cases := []struct {
		name   string
		method string
		path   string
		tok    string
		header http.Header
	}{
		{"an ordinary request", "GET", "/api/v1/status", token, nil},
		{"an unauthorized request", "GET", "/api/v1/status", "", nil},
		{"a missing route", "GET", "/api/v1/nothing", token, nil},
		{"a preflight from a page", "OPTIONS", "/api/v1/employees", token, http.Header{"Origin": {"http://evil.example"}, "Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"authorization"}}},
		{"a preflight without a token", "OPTIONS", "/api/v1/employees", "", http.Header{"Origin": {"http://evil.example"}, "Access-Control-Request-Method": {"POST"}}},
		{"an OPTIONS from a program", "OPTIONS", "/api/v1/employees", token, nil},
		{"a request from a page", "GET", "/api/v1/status", token, http.Header{"Origin": {"http://evil.example"}}},
	}
	for _, tc := range cases {
		resp, _ := a.raw(t, tc.method, tc.path, tc.tok, nil, tc.header)
		for k := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s: the response carries %s: the API must never allow browsers in", tc.name, k)
			}
		}
		if tc.method == "OPTIONS" && resp.StatusCode < 400 {
			t.Errorf("%s answered %d: a preflight must not succeed", tc.name, resp.StatusCode)
		}
		if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing hardening headers: %v", tc.name, resp.Header)
		}
	}
}

func TestTurnStreamsOverWebSocketAndCancels(t *testing.T) {
	a := newAPI(t)
	hello := a.employee(t, "Atlas", a.fakeProfile(t, "hello", providertest.Hello, "structured"))
	sleeper := a.employee(t, "Bruno", a.fakeProfile(t, "sleep", providertest.Sleep, "structured"))

	c, _, err := a.dial(t, "/api/v1/events?employee="+hello, nil)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, func(m message) bool { return m.Type == "hello" })

	var turn struct {
		TurnID string `json:"turn_id"`
	}
	a.call(t, "POST", "/api/v1/employees/"+hello+"/turns", map[string]string{"prompt": "ping"}, 202, &turn)
	if turn.TurnID == "" {
		t.Fatal("no turn ID")
	}
	var kinds []string
	var text string
	var last uint64
	for {
		m := readMessage(t, c)
		if m.Type != "update" {
			continue
		}
		u := m.Update
		if u.EmployeeID != hello || u.TurnID != turn.TurnID {
			t.Fatalf("an update for %s/%s reached a viewer of %s/%s", u.EmployeeID, u.TurnID, hello, turn.TurnID)
		}
		if last != 0 && u.Seq != last+1 {
			t.Errorf("update sequence jumped from %d to %d", last, u.Seq)
		}
		last = u.Seq
		kinds = append(kinds, u.Kind)
		if u.Event != nil && u.Event.Kind == "text" {
			text += u.Event.Text
		}
		if u.Kind == "turn_ended" {
			if u.State != "completed" {
				t.Fatalf("the turn ended as %q", u.State)
			}
			break
		}
	}
	if kinds[0] != "turn_queued" || kinds[1] != "turn_started" || text != "pong" {
		t.Fatalf("updates %v, text %q; want queued, started, events, ended and the text pong", kinds, text)
	}

	// Another employee's work does not reach this viewer; it is cancelable.
	d, _, err := a.dial(t, "/api/v1/events?employee="+sleeper, nil)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, d, func(m message) bool { return m.Type == "hello" })
	a.call(t, "POST", "/api/v1/employees/"+sleeper+"/turns", map[string]string{"prompt": "wait"}, 202, &turn)
	readUntil(t, d, func(m message) bool { return m.Type == "update" && m.Update.Kind == "turn_started" })
	a.call(t, "POST", "/api/v1/employees/"+sleeper+"/cancel", nil, 204, nil)
	ended := readUntil(t, d, func(m message) bool { return m.Type == "update" && m.Update.Kind == "turn_ended" })
	if ended.Update.State != "canceled" {
		t.Fatalf("canceled turn ended as %q", ended.Update.State)
	}
	a.call(t, "POST", "/api/v1/employees/"+sleeper+"/cancel", nil, 409, nil) // nothing to cancel any more

	// History and stored events are available afterwards.
	var hist []struct {
		ID, State, Prompt string
		InputTokens       int `json:"input_tokens"`
	}
	a.call(t, "GET", "/api/v1/employees/"+hello+"/history", nil, 200, &hist)
	if len(hist) != 1 || hist[0].State != "completed" || hist[0].Prompt != "ping" || hist[0].InputTokens == 0 {
		t.Fatalf("history = %+v", hist)
	}
	var events []struct{ Kind, Text string }
	a.call(t, "GET", "/api/v1/turns/"+hist[0].ID+"/events", nil, 200, &events)
	if len(events) == 0 || events[len(events)-1].Kind != "done" {
		t.Fatalf("stored events = %+v", events)
	}
}

func TestApprovalFlowOverAPI(t *testing.T) {
	a := newAPI(t)
	c, _, err := a.dial(t, "/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, func(m message) bool { return m.Type == "hello" })

	ask := func(target string) (chan string, *struct{}) {
		out := make(chan string, 1)
		go func() {
			d, why, _ := a.broker.Check(context.Background(), actionFor("emp-1", target))
			out <- string(d) + "/" + string(why)
		}()
		return out, nil
	}
	first, _ := ask("git status")
	m := readUntil(t, c, func(m message) bool { return m.Type == "approval" })
	if m.Approval.EmployeeID != "emp-1" || m.Approval.Kind != "run_command" || m.Approval.Target != "git status" {
		t.Fatalf("approval = %+v", m.Approval)
	}
	var pending []struct{ ID, Target string }
	a.call(t, "GET", "/api/v1/approvals", nil, 200, &pending)
	if len(pending) != 1 || pending[0].ID != m.Approval.ID {
		t.Fatalf("pending = %+v", pending)
	}

	a.call(t, "POST", "/api/v1/approvals/"+m.Approval.ID, map[string]any{"allow": true, "remember": "exact"}, 204, nil)
	select {
	case got := <-first:
		if got != "allowed/user" {
			t.Fatalf("decision = %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the action never got its answer")
	}
	a.call(t, "POST", "/api/v1/approvals/"+m.Approval.ID, map[string]any{"allow": true}, 404, nil) // already answered
	a.call(t, "POST", "/api/v1/approvals/x", map[string]any{"allow": true, "remember": "forever"}, 400, nil)

	// Remembered: the same action passes without a question.
	second, _ := ask("git status")
	if got := <-second; got != "allowed/remembered" {
		t.Fatalf("second time = %s", got)
	}
	var grants []struct {
		Kind, Target string
		Allow        bool
	}
	a.call(t, "GET", "/api/v1/employees/emp-1/grants", nil, 200, &grants)
	if len(grants) != 1 || grants[0].Target != "git status" || !grants[0].Allow {
		t.Fatalf("grants = %+v", grants)
	}
	a.call(t, "PUT", "/api/v1/employees/emp-1/grants", map[string]any{"kind": "network", "target": "*", "allow": false}, 204, nil)
	a.call(t, "DELETE", "/api/v1/employees/emp-1/grants?kind=run_command&target="+"git%20status", nil, 204, nil)
	a.call(t, "GET", "/api/v1/employees/emp-1/grants", nil, 200, &grants)
	if len(grants) != 1 || grants[0].Kind != "network" {
		t.Fatalf("grants after the edit = %+v", grants)
	}
}

// readBinary reads binary frames until the text appears.
func readBinary(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var got string
	for !strings.Contains(got, want) {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", want, err, got)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("a terminal sends binary frames, got %v", typ)
		}
		got += string(b)
	}
	return got
}

func TestTerminalStreamsOverWebSocket(t *testing.T) {
	a := newAPI(t)
	emp := a.employee(t, "Atlas", a.fakeProfile(t, "tui", providertest.TUI, "terminal"))

	// Not running yet: the socket is refused with a clear error.
	if _, resp, err := a.dial(t, "/api/v1/employees/"+emp+"/terminal/ws", nil); err == nil || resp == nil || resp.StatusCode != 409 {
		t.Fatalf("attaching to a terminal that is not running: %v / %v, want 409", resp, err)
	}
	a.call(t, "POST", "/api/v1/employees/"+emp+"/terminal", map[string]int{"rows": 30, "cols": 100}, 200, nil)
	var info struct {
		TerminalRunning bool `json:"terminal_running"`
		Mode            string
	}
	a.call(t, "GET", "/api/v1/employees/"+emp, nil, 200, &info)
	if !info.TerminalRunning || info.Mode != "terminal" {
		t.Fatalf("employee = %+v", info)
	}

	c, _, err := a.dial(t, "/api/v1/employees/"+emp+"/terminal/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	readBinary(t, c, "FAKE-TUI ready") // the replay comes first
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	readBinary(t, c, "you said: hello")
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.Write(ctx, websocket.MessageBinary, []byte("/size\r")); err != nil {
		t.Fatal(err)
	}
	readBinary(t, c, "size:40x120")

	// The viewer leaves; the program keeps running; a new viewer gets the replay.
	_ = c.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)
	a.call(t, "GET", "/api/v1/employees/"+emp, nil, 200, &info)
	if !info.TerminalRunning {
		t.Fatal("the terminal must keep running when the viewer leaves")
	}
	d, _, err := a.dial(t, "/api/v1/employees/"+emp+"/terminal/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readBinary(t, d, "you said: hello"); !strings.Contains(got, "FAKE-TUI") {
		t.Fatalf("the replay lacks the start of the session: %q", got)
	}

	// Stopping it ends the stream.
	a.call(t, "DELETE", "/api/v1/employees/"+emp+"/terminal", nil, 204, nil)
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if _, _, err := d.Read(ctx2); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("the stream should end with a normal close, got %v", err)
			}
			break
		}
	}
}

func TestLoginTerminalOverWebSocket(t *testing.T) {
	a := newAPI(t)
	profile := a.fakeProfile(t, "login", providertest.Hello, "structured")
	// The fake CLI starts logged out.
	var det struct {
		Login string
		Modes []string
	}
	a.call(t, "GET", "/api/v1/profiles/"+profile+"/detect", nil, 200, &det)
	if det.Login != "logged_out" {
		t.Fatalf("detection = %+v, want logged out", det)
	}
	a.call(t, "POST", "/api/v1/profiles/"+profile+"/login", nil, 200, nil)
	c, _, err := a.dial(t, "/api/v1/profiles/"+profile+"/login/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	readBinary(t, c, "Press Enter to log in")
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	readBinary(t, c, "Logged in")
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.call(t, "GET", "/api/v1/profiles/"+profile+"/detect", nil, 200, &det)
		if det.Login == "logged_in" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the login flow the profile is %q", det.Login)
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.call(t, "POST", "/api/v1/profiles/nope/login", nil, 404, nil)
}

func TestAPIKeyIsNeverReturned(t *testing.T) {
	a := newAPI(t)
	const secret = "sk-very-secret-key-1234567890"
	var p struct {
		ID string `json:"id"`
	}
	a.call(t, "POST", "/api/v1/profiles", map[string]any{"kind": "openai-api", "name": "api", "mode": "api", "model": "gpt-x", "base_url": "http://127.0.0.1:1/v1"}, 201, &p)

	var det struct{ Login string }
	a.call(t, "GET", "/api/v1/profiles/"+p.ID+"/detect", nil, 200, &det)
	if det.Login != "logged_out" {
		t.Fatalf("without a key: %q", det.Login)
	}
	a.call(t, "PUT", "/api/v1/profiles/"+p.ID+"/api-key", map[string]string{"key": secret}, 204, nil)
	a.call(t, "GET", "/api/v1/profiles/"+p.ID+"/detect", nil, 200, &det)
	if det.Login != "logged_in" {
		t.Fatalf("with a key: %q", det.Login)
	}

	// The key sits in the credential store and nowhere an API answer can reach.
	if v, _ := a.keys.Get("profile/" + p.ID + "/api-key"); v != secret {
		t.Fatalf("the credential store holds %q", v)
	}
	for _, path := range []string{"/api/v1/profiles", "/api/v1/profiles/" + p.ID + "/detect", "/api/v1/employees"} {
		if _, b := a.raw(t, "GET", path, token, nil, nil); strings.Contains(string(b), secret) {
			t.Errorf("GET %s returned the API key", path)
		}
	}
	rows, _ := a.st.Audit(context.Background(), "", 0)
	for _, r := range rows {
		if strings.Contains(r.Detail+r.Action, secret) {
			t.Errorf("the audit log holds the API key: %+v", r)
		}
	}
	a.call(t, "PUT", "/api/v1/profiles/"+p.ID+"/api-key", map[string]string{"key": "  "}, 500, nil)
	a.call(t, "DELETE", "/api/v1/profiles/"+p.ID+"/api-key", nil, 204, nil)
	a.call(t, "GET", "/api/v1/profiles/"+p.ID+"/detect", nil, 200, &det)
	if det.Login != "logged_out" {
		t.Fatalf("after removing the key: %q", det.Login)
	}
}

func TestErrorsAreClearAndBounded(t *testing.T) {
	a := newAPI(t)
	profile := a.fakeProfile(t, "p", providertest.Sleep, "structured")
	emp := a.employee(t, "Atlas", profile)

	check := func(method, path string, body any, wantStatus int, wantCode string) {
		t.Helper()
		resp, b := a.raw(t, method, path, token, body, nil)
		if resp.StatusCode != wantStatus || !strings.Contains(string(b), `"code":"`+wantCode+`"`) {
			t.Errorf("%s %s = %d %s; want %d %s", method, path, resp.StatusCode, b, wantStatus, wantCode)
		}
	}
	check("GET", "/api/v1/employees/nobody", nil, 404, "not_found")
	check("POST", "/api/v1/employees", map[string]any{"name": "Atlas", "profile_id": profile}, 409, "name_taken")
	check("POST", "/api/v1/employees", map[string]any{"name": "", "profile_id": profile}, 400, "invalid")
	check("POST", "/api/v1/employees", map[string]any{"name": "Zed", "profile_id": "claude-nope-0000"}, 404, "profile_not_found")
	check("POST", "/api/v1/employees", map[string]any{"name": "Zed", "profile_id": profile, "surprise": 1}, 400, "bad_json")
	check("POST", "/api/v1/employees", []byte("{not json"), 400, "bad_json")
	check("POST", "/api/v1/employees", []byte(`{"name":"`+strings.Repeat("x", 2<<20)+`"}`), 413, "too_large")
	check("POST", "/api/v1/employees/"+emp+"/turns", map[string]string{"prompt": ""}, 400, "invalid")
	check("POST", "/api/v1/employees/nobody/turns", map[string]string{"prompt": "hi"}, 404, "not_found")
	check("GET", "/api/v1/employees/"+emp+"/history?before=yesterday", nil, 400, "invalid")
	check("DELETE", "/api/v1/profiles/"+profile, nil, 409, "profile_in_use")
	check("POST", "/api/v1/profiles", map[string]any{"kind": "alien", "name": "x", "binary": "/bin/x"}, 400, "no_provider")
	check("POST", "/api/v1/profiles/"+profile+"/notices", map[string]string{"notice": "made-up"}, 400, "unknown_notice")
	check("POST", "/api/v1/employees/"+emp+"/terminal", nil, 409, "not_terminal")

	a.call(t, "POST", "/api/v1/employees/"+emp+"/turns", map[string]string{"prompt": "work"}, 202, nil)
	check("POST", "/api/v1/employees/"+emp+"/turns", map[string]string{"prompt": "more"}, 409, "busy")
	a.call(t, "POST", "/api/v1/employees/"+emp+"/pause", nil, 204, nil)
	check("POST", "/api/v1/employees/"+emp+"/turns", map[string]string{"prompt": "more"}, 409, "paused")
	a.call(t, "POST", "/api/v1/employees/"+emp+"/resume", nil, 204, nil)

	// A structured profile that has not accepted its notice cannot start.
	var p struct{ ID string }
	a.call(t, "POST", "/api/v1/profiles", map[string]any{"kind": "claude", "name": "unaccepted", "binary": "/bin/true", "mode": "structured"}, 201, &p)
	e2 := a.employee(t, "Bruno", p.ID)
	check("POST", "/api/v1/employees/"+e2+"/turns", map[string]string{"prompt": "hi"}, 409, "notice_required")

	a.call(t, "DELETE", "/api/v1/employees/"+emp, nil, 204, nil)
	check("GET", "/api/v1/employees/"+emp, nil, 404, "not_found")
}

func TestMemoryOverAPI(t *testing.T) {
	a := newAPI(t)
	emp := a.employee(t, "Atlas", a.fakeProfile(t, "p", providertest.Hello, "structured"))
	other := a.employee(t, "Bruno", a.fakeProfile(t, "q", providertest.Hello, "structured"))

	var created struct{ ID int64 }
	a.call(t, "POST", "/api/v1/employees/"+emp+"/memory/facts", map[string]string{"key": "deploys", "body": "Deploys happen on Tuesdays"}, 201, &created)
	a.call(t, "POST", "/api/v1/employees/"+emp+"/memory/facts", map[string]string{"body": " "}, 400, nil)
	var facts []struct {
		ID   int64
		Body string
	}
	a.call(t, "GET", "/api/v1/employees/"+emp+"/memory/facts", nil, 200, &facts)
	if len(facts) != 1 || facts[0].ID != created.ID {
		t.Fatalf("facts = %+v", facts)
	}

	// A note the user writes by hand in the employee's memory folder is found.
	var e struct{ Name string }
	a.call(t, "GET", "/api/v1/employees/"+emp, nil, 200, &e)
	notes := a.layout.EmployeeDir("atlas") + "/memory"
	writeFile(t, notes+"/plan.md", "The migration is planned for November")
	var res struct{ Indexed int }
	a.call(t, "POST", "/api/v1/employees/"+emp+"/memory/sync", nil, 200, &res)
	if res.Indexed != 1 {
		t.Fatalf("sync = %+v", res)
	}
	var hits []struct{ Kind, Ref, Snippet string }
	a.call(t, "GET", "/api/v1/employees/"+emp+"/memory/search?q=tuesdays+deploys", nil, 200, &hits)
	if len(hits) != 1 || hits[0].Kind != "fact" {
		t.Fatalf("hits = %+v", hits)
	}
	a.call(t, "GET", "/api/v1/employees/"+emp+"/memory/search?q=migration", nil, 200, &hits)
	if len(hits) != 1 || hits[0].Kind != "note" || hits[0].Ref != "plan.md" {
		t.Fatalf("hits = %+v", hits)
	}
	// Another employee sees none of it.
	a.call(t, "GET", "/api/v1/employees/"+other+"/memory/search?q=migration", nil, 200, &hits)
	if len(hits) != 0 {
		t.Fatalf("another employee found %+v", hits)
	}
	a.call(t, "DELETE", "/api/v1/employees/"+other+"/memory/facts/"+itoa(created.ID), nil, 404, nil)
	a.call(t, "DELETE", "/api/v1/employees/"+emp+"/memory/facts/"+itoa(created.ID), nil, 204, nil)
	a.call(t, "DELETE", "/api/v1/employees/"+emp+"/memory/facts/abc", nil, 400, nil)
}
