package permissions

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/store"
)

func newBroker(t *testing.T) (*Broker, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := New(st)
	b.Timeout = 150 * time.Millisecond // nobody answers unless a test does
	return b, st
}

func auditOf(t *testing.T, st *store.Store, employee string) []store.AuditRow {
	t.Helper()
	rows, err := st.Audit(context.Background(), employee, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSensitiveActionsDeniedByDefault(t *testing.T) {
	b, st := newBroker(t)
	ctx := context.Background()

	for _, k := range []Kind{RunCommand, WriteOutside, ReadOutside, Network, Kind("something_new")} {
		d, why, err := b.Check(ctx, Action{EmployeeID: "e1", Kind: k, Target: "x"})
		if err != nil || d != Denied || why != ReasonTimeout {
			t.Errorf("%s: %s/%s/%v, want denied because nobody approved", k, d, why, err)
		}
	}
	// Safe actions inside the employee's folder pass without asking.
	for _, k := range []Kind{ReadInside, WriteInside} {
		if d, why, err := b.Check(ctx, Action{EmployeeID: "e1", Kind: k, Target: "notes.md"}); err != nil || d != Allowed || why != ReasonSafe {
			t.Errorf("%s: %s/%s/%v, want allowed as safe", k, d, why, err)
		}
	}
	if _, _, err := b.Check(ctx, Action{Kind: RunCommand, Target: "ls"}); err == nil {
		t.Error("an action without an employee must be an error")
	}
	if got := len(auditOf(t, st, "e1")); got != 5 {
		t.Errorf("%d audit entries, want one per sensitive action (5) and none for safe ones", got)
	}

	// A request waits for the user instead of failing at once, and shows up
	// for the UI.
	b.Timeout = 5 * time.Second
	sub, cancel := b.Subscribe()
	defer cancel()
	done := make(chan Decision, 1)
	go func() {
		d, _, _ := b.Check(ctx, Action{EmployeeID: "e1", Kind: RunCommand, Target: "git status"})
		done <- d
	}()
	var req Request
	select {
	case req = <-sub:
	case <-time.After(3 * time.Second):
		t.Fatal("the UI was not told about the request")
	}
	if got := b.Pending(); len(got) != 1 || got[0].ID != req.ID || got[0].Action.Target != "git status" {
		t.Fatalf("Pending = %+v", got)
	}
	select {
	case d := <-done:
		t.Fatalf("the action must wait for the user, but returned %s", d)
	case <-time.After(100 * time.Millisecond):
	}
	if err := b.Answer(req.ID, true, RememberNone); err != nil {
		t.Fatal(err)
	}
	if d := <-done; d != Allowed {
		t.Fatalf("after approval: %s", d)
	}
	if err := b.Answer(req.ID, true, RememberNone); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("answering twice = %v, want ErrUnknownRequest", err)
	}
	if len(b.Pending()) != 0 {
		t.Fatal("an answered request is no longer pending")
	}
}

// ask runs Check in the background and answers it as soon as it is pending.
func ask(t *testing.T, b *Broker, a Action, allow bool, remember Remember) (Decision, Reason) {
	t.Helper()
	b.Timeout = 5 * time.Second
	sub, cancel := b.Subscribe()
	defer cancel()
	type result struct {
		d Decision
		r Reason
	}
	out := make(chan result, 1)
	go func() {
		d, r, _ := b.Check(context.Background(), a)
		out <- result{d, r}
	}()
	select {
	case req := <-sub:
		if err := b.Answer(req.ID, allow, remember); err != nil {
			t.Fatal(err)
		}
	case res := <-out: // decided without asking
		return res.d, res.r
	case <-time.After(3 * time.Second):
		t.Fatal("no request appeared")
	}
	res := <-out
	return res.d, res.r
}

func TestRememberedDecisionIsScopedToEmployeeAndAction(t *testing.T) {
	b, _ := newBroker(t)
	ctx := context.Background()
	b.Timeout = 100 * time.Millisecond

	// The user allows "git status" for Atlas and asks to remember it.
	if d, why := ask(t, b, Action{EmployeeID: "atlas", Kind: RunCommand, Target: "git status"}, true, RememberExact); d != Allowed || why != ReasonUser {
		t.Fatalf("first answer: %s/%s", d, why)
	}
	b.Timeout = 100 * time.Millisecond

	check := func(a Action) (Decision, Reason) {
		d, r, err := b.Check(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return d, r
	}
	// Same employee, same action: no question, allowed.
	if d, r := check(Action{EmployeeID: "atlas", Kind: RunCommand, Target: "git status"}); d != Allowed || r != ReasonRemembered {
		t.Errorf("the remembered action: %s/%s, want allowed/remembered", d, r)
	}
	// Anything that differs is a new question, which nobody answers: denied.
	for name, a := range map[string]Action{
		"another employee": {EmployeeID: "bruno", Kind: RunCommand, Target: "git status"},
		"another target":   {EmployeeID: "atlas", Kind: RunCommand, Target: "git push"},
		"another kind":     {EmployeeID: "atlas", Kind: Network, Target: "git status"},
		"a similar target": {EmployeeID: "atlas", Kind: RunCommand, Target: "git status "},
		"a longer target":  {EmployeeID: "atlas", Kind: RunCommand, Target: "git status && rm -rf ~"},
	} {
		if d, r := check(a); d != Denied || r != ReasonTimeout {
			t.Errorf("%s: %s/%s, want denied as an unanswered new question", name, d, r)
		}
	}

	// "Always for this kind" covers every target of that kind for that
	// employee only.
	b.Timeout = 5 * time.Second
	if d, _ := ask(t, b, Action{EmployeeID: "atlas", Kind: Network, Target: "example.com"}, true, RememberKind); d != Allowed {
		t.Fatal("the answer must be allowed")
	}
	b.Timeout = 100 * time.Millisecond
	if d, r := check(Action{EmployeeID: "atlas", Kind: Network, Target: "other.org"}); d != Allowed || r != ReasonRemembered {
		t.Errorf("kind-wide grant: %s/%s", d, r)
	}
	if d, _ := check(Action{EmployeeID: "bruno", Kind: Network, Target: "other.org"}); d != Denied {
		t.Error("a kind-wide grant must not reach another employee")
	}
	if d, _ := check(Action{EmployeeID: "atlas", Kind: RunCommand, Target: "curl other.org"}); d != Denied {
		t.Error("a kind-wide grant must not reach another kind")
	}

	// A specific "deny" beats the kind-wide "allow", and is applied without
	// asking. (With the broad allow in place nobody is asked, so the specific
	// deny comes from the settings screen.)
	if err := b.Remember(ctx, "atlas", Network, "evil.example", false); err != nil {
		t.Fatal(err)
	}
	if d, r := check(Action{EmployeeID: "atlas", Kind: Network, Target: "evil.example"}); d != Denied || r != ReasonRemembered {
		t.Errorf("remembered deny: %s/%s", d, r)
	}
	if d, _ := check(Action{EmployeeID: "atlas", Kind: Network, Target: "fine.example"}); d != Allowed {
		t.Error("other targets keep the broad allow")
	}

	// The user can see what is remembered and take it back.
	list, err := b.Remembered(ctx, "atlas")
	if err != nil || len(list) != 3 {
		t.Fatalf("remembered = %+v, %v; want the 3 decisions above", list, err)
	}
	if err := b.Forget(ctx, "atlas", Network, "*"); err != nil {
		t.Fatal(err)
	}
	if d, _ := check(Action{EmployeeID: "atlas", Kind: Network, Target: "fine.example"}); d != Denied {
		t.Error("after forgetting the broad allow, the question comes back (and is denied unanswered)")
	}
	if err := b.Remember(ctx, "", Network, "x", true); err == nil {
		t.Error("a decision without an employee must be refused")
	}
}

func TestAuditLogHasNoUpdateOrDelete(t *testing.T) {
	b, st := newBroker(t)
	ctx := context.Background()
	if _, _, err := b.Check(ctx, Action{EmployeeID: "e1", Kind: RunCommand, Target: "ls"}); err != nil {
		t.Fatal(err)
	}

	// 1. The code exposes no way to change or remove an audit entry.
	for _, rt := range []reflect.Type{reflect.TypeOf(st), reflect.TypeOf(b)} {
		for i := 0; i < rt.NumMethod(); i++ {
			name := rt.Method(i).Name
			if strings.Contains(name, "Audit") && name != "AppendAudit" && name != "Audit" {
				t.Errorf("%s.%s: the audit log may only be appended to and read", rt, name)
			}
			if strings.Contains(strings.ToLower(name), "audit") && (strings.Contains(name, "Update") || strings.Contains(name, "Delete") || strings.Contains(name, "Remove") || strings.Contains(name, "Clear")) {
				t.Errorf("%s.%s must not exist", rt, name)
			}
		}
	}
	// 2. The database itself refuses it.
	if _, err := st.DB().ExecContext(ctx, `UPDATE audit_log SET decision = 'allowed'`); err == nil {
		t.Error("the database accepted an UPDATE of the audit log")
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("the database accepted a DELETE from the audit log")
	}
	rows := auditOf(t, st, "e1")
	if len(rows) != 1 || rows[0].Decision != string(Denied) || !strings.Contains(rows[0].Detail, "ls") || rows[0].Kind != "permission" {
		t.Fatalf("audit = %+v", rows)
	}
}

func TestAuditFailureDeniesTheAction(t *testing.T) {
	b, st := newBroker(t)
	ctx := context.Background()
	// Allow the action in advance, then break the audit log.
	if err := st.PutGrant(ctx, store.GrantRow{EmployeeID: "e1", Kind: string(RunCommand), Target: "ls", Allow: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `ALTER TABLE audit_log RENAME TO audit_log_gone`); err != nil {
		t.Fatal(err)
	}
	d, why, err := b.Check(ctx, Action{EmployeeID: "e1", Kind: RunCommand, Target: "ls"})
	if d != Denied || why != ReasonAuditError || err == nil {
		t.Fatalf("%s/%s/%v: with no way to record the action it must be denied, even though it was allowed before", d, why, err)
	}
}

func TestApprovalTimeoutDenies(t *testing.T) {
	b, st := newBroker(t)
	ctx := context.Background()

	start := time.Now()
	d, why, err := b.Check(ctx, Action{EmployeeID: "e1", Kind: WriteOutside, Target: "/etc/hosts"})
	if err != nil || d != Denied || why != ReasonTimeout {
		t.Fatalf("%s/%s/%v, want denied by timeout", d, why, err)
	}
	if took := time.Since(start); took < 100*time.Millisecond || took > 3*time.Second {
		t.Errorf("waited %s, want about the configured timeout", took)
	}
	if len(b.Pending()) != 0 {
		t.Error("an expired request must not stay pending")
	}
	// Answering a request that already expired fails cleanly.
	if err := b.Answer("req-late", true, RememberNone); !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("late answer = %v", err)
	}
	rows := auditOf(t, st, "e1")
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, "timeout") || rows[0].Decision != "denied" {
		t.Fatalf("the timeout must be audited: %+v", rows)
	}

	// Canceling the turn while the request waits denies it and records that.
	b.Timeout = time.Minute
	cctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var got Reason
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, got, _ = b.Check(cctx, Action{EmployeeID: "e2", Kind: Network, Target: "example.com"})
	}()
	for len(b.Pending()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if got != ReasonCanceled {
		t.Fatalf("reason = %s, want canceled", got)
	}
	if rows := auditOf(t, st, "e2"); len(rows) != 1 || !strings.Contains(rows[0].Detail, "canceled") {
		t.Fatalf("the abandoned request must be in the audit log: %+v", rows)
	}
}
