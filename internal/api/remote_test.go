package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aotus/internal/netaccess"
	"aotus/internal/permissions"
	"aotus/internal/provider/providertest"
)

const (
	ownerLogin = "owner@example.com"
	anaLogin   = "ana@example.com"
	tailHost   = "vps.tail1234.ts.net:7843"
)

// asTailnet sends a request the way the tailnet listener delivers it: the
// connection already carries the identity Tailscale vouched for. No token.
func (a *testAPI) asTailnet(t *testing.T, login, device, method, path string, body any, mutate func(*http.Request)) (int, []byte) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(context.Background(), method, "http://"+tailHost+path, rd)
	req.Host = tailHost
	req = req.WithContext(netaccess.WithCaller(context.Background(), netaccess.Caller{Method: netaccess.MethodTailnet, Login: login, Device: device}))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// setUpAccess records the owner and allows ana, as the owner would.
func (a *testAPI) setUpAccess(t *testing.T) {
	t.Helper()
	if err := a.st.SetOwnerLogin(context.Background(), ownerLogin); err != nil {
		t.Fatal(err)
	}
	a.call(t, "POST", "/api/v1/access", map[string]any{"login": anaLogin, "acknowledged": true}, 204, nil)
}

func TestTailnetCallerOnAllowListIsServed(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)
	for _, who := range []string{ownerLogin, anaLogin, "ANA@example.com"} {
		if code, b := a.asTailnet(t, who, "laptop", "GET", "/api/v1/employees", nil, nil); code != 200 {
			t.Errorf("%s: GET /employees = %d %s", who, code, b)
		}
	}
	code, b := a.asTailnet(t, anaLogin, "phone", "GET", "/api/v1/me", nil, nil)
	var me struct{ Login, Device, Role, Method string }
	_ = json.Unmarshal(b, &me)
	if code != 200 || me.Login != anaLogin || me.Device != "phone" || me.Role != "guest" || me.Method != "tailnet" {
		t.Fatalf("/me = %d %+v", code, me)
	}
	_, b = a.asTailnet(t, ownerLogin, "pc", "GET", "/api/v1/me", nil, nil)
	_ = json.Unmarshal(b, &me)
	if me.Role != "owner" {
		t.Fatalf("the owner's role = %q", me.Role)
	}
}

func TestTailnetCallerOffAllowListIsRefusedAndAudited(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)
	code, b := a.asTailnet(t, "eve@example.com", "evil-laptop", "GET", "/api/v1/employees", nil, nil)
	if code != 403 || !strings.Contains(string(b), "not_allowed") {
		t.Fatalf("an unlisted login got %d %s, want 403 not_allowed", code, b)
	}
	rows, err := a.st.Audit(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Kind == "access" && r.Action == "refused" && r.Decision == "denied" &&
			strings.Contains(r.Caller, "eve@example.com") && strings.Contains(r.Caller, "evil-laptop") && strings.Contains(r.Detail, "/api/v1/employees") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal was not audited with the caller: %+v", rows)
	}

	// With no owner recorded, nobody is served, not even a plausible login.
	b2 := newAPI(t)
	if code, _ := b2.asTailnet(t, ownerLogin, "pc", "GET", "/api/v1/employees", nil, nil); code != 403 {
		t.Fatalf("with no owner recorded a remote caller got %d, want 403", code)
	}
}

func TestTokenAndIdentityDoNotCrossOver(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)

	// The token does not turn an unlisted tailnet caller into a served one.
	code, _ := a.asTailnet(t, "eve@example.com", "x", "GET", "/api/v1/employees", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != 403 {
		t.Fatalf("token on the tailnet by an unlisted login = %d, want 403", code)
	}
	// A listed tailnet caller needs no token, and sending a wrong one changes nothing.
	code, _ = a.asTailnet(t, anaLogin, "x", "GET", "/api/v1/employees", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer wrong")
	})
	if code != 200 {
		t.Fatalf("a listed login with a wrong token = %d, want 200 (the token is not used on the tailnet)", code)
	}

	// On loopback, claiming an identity in headers does nothing: the token is required.
	for _, h := range []string{"X-Tailscale-User-Login", "Tailscale-User-Login", "X-Forwarded-User", "X-Aotus-Login"} {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:1/api/v1/employees", nil)
		req.Host = "127.0.0.1:1"
		req.Header.Set(h, ownerLogin)
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Errorf("header %s on loopback without a token = %d, want 401", h, rec.Code)
		}
	}
	// A caller that is not a tailnet caller cannot be fabricated into one by a header on the tailnet either.
	code, _ = a.asTailnet(t, "eve@example.com", "x", "GET", "/api/v1/employees", nil, func(r *http.Request) {
		r.Header.Set("X-Tailscale-User-Login", ownerLogin)
	})
	if code != 403 {
		t.Fatalf("an identity header must not raise an unlisted caller: %d", code)
	}
}

func TestAuditRecordsTheCaller(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)
	profile := a.fakeProfile(t, "tui", providertest.TUI, "terminal")
	code, b := a.asTailnet(t, ownerLogin, "pc", "POST", "/api/v1/employees", map[string]any{"name": "Atlas", "profile_id": profile}, nil)
	if code != 201 {
		t.Fatalf("owner creates an employee: %d %s", code, b)
	}
	var emp struct{ ID string }
	_ = json.Unmarshal(b, &emp)
	rows, _ := a.st.Audit(context.Background(), emp.ID, 0)
	if len(rows) == 0 || !strings.Contains(rows[0].Caller, ownerLogin) || !strings.Contains(rows[0].Caller, "pc") {
		t.Fatalf("creation audit rows = %+v, want the caller", rows)
	}

	// An approval answered by ana is recorded as hers.
	a.call(t, "POST", "/api/v1/profiles/"+profile+"/shares", map[string]any{"login": anaLogin}, 204, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = a.broker.Check(context.Background(), permissions.Action{EmployeeID: emp.ID, Kind: permissions.RunCommand, Target: "ls"})
	}()
	var req permissions.Request
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p := a.broker.Pending(); len(p) > 0 {
			req = p[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req.ID == "" {
		t.Fatal("no approval was requested")
	}
	if code, b := a.asTailnet(t, anaLogin, "ana-phone", "POST", "/api/v1/approvals/"+req.ID, map[string]any{"allow": true}, nil); code != 204 {
		t.Fatalf("ana answers: %d %s", code, b)
	}
	<-done
	rows, _ = a.st.Audit(context.Background(), emp.ID, 0)
	var ok bool
	for _, r := range rows {
		if r.Kind == "permission" && r.Decision == "allowed" && strings.Contains(r.Caller, anaLogin) && strings.Contains(r.Caller, "ana-phone") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the approval answer was not attributed to ana: %+v", rows)
	}
}

func TestSecondPersonNeedsAcknowledgement(t *testing.T) {
	a := newAPI(t)
	if err := a.st.SetOwnerLogin(context.Background(), ownerLogin); err != nil {
		t.Fatal(err)
	}
	// No acknowledgement, no entry.
	a.call(t, "POST", "/api/v1/access", map[string]any{"login": anaLogin}, 400, nil)
	a.call(t, "POST", "/api/v1/access", map[string]any{"login": anaLogin, "acknowledged": false}, 400, nil)
	if list, _ := a.st.AccessList(context.Background()); len(list) != 0 {
		t.Fatalf("nothing may be stored without the acknowledgement: %+v", list)
	}
	a.call(t, "POST", "/api/v1/access", map[string]any{"login": ownerLogin, "acknowledged": true}, 409, nil)
	a.call(t, "POST", "/api/v1/access", map[string]any{"login": "Ana@Example.com", "acknowledged": true}, 204, nil)
	list, _ := a.st.AccessList(context.Background())
	if len(list) != 1 || list[0].Login != anaLogin || !strings.Contains(list[0].Notice, anaLogin) || !strings.Contains(list[0].Notice, "terms") {
		t.Fatalf("the stored acknowledgement must name the person and the risk: %+v", list)
	}
	var shown struct {
		Notice  string `json:"sharing_notice"`
		Owner   string
		Entries []struct{ Login string }
	}
	a.call(t, "GET", "/api/v1/access", nil, 200, &shown)
	if shown.Owner != ownerLogin || len(shown.Entries) != 1 || shown.Notice == "" {
		t.Fatalf("GET /access = %+v", shown)
	}

	// A guest cannot manage access or subscriptions.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/access"}, {"GET", "/api/v1/access"}, {"POST", "/api/v1/profiles"},
		{"DELETE", "/api/v1/access/" + anaLogin}, {"POST", "/api/v1/profiles/x/login"},
	} {
		if code, _ := a.asTailnet(t, anaLogin, "x", c.method, c.path, map[string]any{"login": "z@example.com", "acknowledged": true}, nil); code != 403 {
			t.Errorf("guest %s %s = %d, want 403", c.method, c.path, code)
		}
	}

	// A guest uses a profile only after the owner shared it.
	profile := a.fakeProfile(t, "tui", providertest.TUI, "terminal")
	emp := a.employee(t, "Atlas", profile)
	if code, b := a.asTailnet(t, anaLogin, "x", "POST", "/api/v1/employees/"+emp+"/terminal", nil, nil); code != 403 || !strings.Contains(string(b), "profile_not_shared") {
		t.Fatalf("a guest starting a terminal on an unshared profile: %d %s", code, b)
	}
	if code, b := a.asTailnet(t, anaLogin, "x", "POST", "/api/v1/employees", map[string]any{"name": "Mine", "profile_id": profile}, nil); code != 403 {
		t.Fatalf("a guest creating an employee on an unshared profile: %d %s", code, b)
	}
	a.call(t, "POST", "/api/v1/profiles/"+profile+"/shares", map[string]any{"login": "nobody@example.com"}, 400, nil) // not on the list
	a.call(t, "POST", "/api/v1/profiles/"+profile+"/shares", map[string]any{"login": anaLogin}, 204, nil)
	if code, b := a.asTailnet(t, anaLogin, "x", "POST", "/api/v1/employees/"+emp+"/terminal", nil, nil); code != 200 {
		t.Fatalf("after sharing, the guest starts the terminal: %d %s", code, b)
	}
	// Taking the person off the list withdraws everything.
	a.call(t, "DELETE", "/api/v1/access/"+anaLogin, nil, 204, nil)
	if code, _ := a.asTailnet(t, anaLogin, "x", "GET", "/api/v1/employees", nil, nil); code != 403 {
		t.Fatalf("a removed person got %d, want 403", code)
	}
	if ok, _ := a.st.ProfileShared(context.Background(), profile, anaLogin); ok {
		t.Fatal("removing a person must forget what was shared with them")
	}
}

func TestRebindingAndOriginStillRefusedOnTailnet(t *testing.T) {
	a := newAPI(t)
	a.setUpAccess(t)
	for _, host := range []string{"evil.example.com", "127.0.0.1:7843", "localhost", "vps.tail1234.ts.net.evil.com", ""} {
		code, b := a.asTailnet(t, ownerLogin, "pc", "GET", "/api/v1/employees", nil, func(r *http.Request) { r.Host = host })
		if code != 403 || !strings.Contains(string(b), "bad_host") {
			t.Errorf("Host %q = %d %s, want 403 bad_host", host, code, b)
		}
	}
	for _, host := range []string{"vps.tail1234.ts.net", "VPS.tail1234.ts.net:7843", "vps.tail1234.ts.net.", "100.64.0.7:7843"} {
		if code, b := a.asTailnet(t, ownerLogin, "pc", "GET", "/api/v1/employees", nil, func(r *http.Request) { r.Host = host }); code != 200 {
			t.Errorf("Host %q = %d %s, want 200", host, code, b)
		}
	}
	code, b := a.asTailnet(t, ownerLogin, "pc", "GET", "/api/v1/employees", nil, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if code != 403 || !strings.Contains(string(b), "bad_origin") {
		t.Fatalf("a web page on the tailnet = %d %s, want 403 bad_origin", code, b)
	}
}
