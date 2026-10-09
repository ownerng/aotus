package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func profileRow(id string) ProfileRow {
	return ProfileRow{ID: id, Name: id, Kind: "claude", Binary: "/bin/claude", ConfigDir: "/data/profiles/" + id}
}

func employeeRow(id, slug, profile string) EmployeeRow {
	now := time.Now()
	return EmployeeRow{ID: id, Slug: slug, Name: slug, ProfileID: profile, State: StateActive, CreatedAt: now, UpdatedAt: now}
}

func TestProfilesRoundTripAndAreProtectedWhileInUse(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	p := profileRow("p1")
	p.AcceptedNotices, p.ExtraEnv, p.APIKeyRef = []string{"n"}, map[string]string{"A": "b"}, "profile/p1/api-key"
	if err := s.PutProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Profile(ctx, "p1")
	if err != nil || got.APIKeyRef != p.APIKeyRef || got.ExtraEnv["A"] != "b" || got.AcceptedNotices[0] != "n" || got.CreatedAt.IsZero() {
		t.Fatalf("profile = %+v, %v", got, err)
	}
	p.Model = "changed"
	if err := s.PutProfile(ctx, p); err != nil { // update in place
		t.Fatal(err)
	}
	if got, _ := s.Profile(ctx, "p1"); got.Model != "changed" {
		t.Fatalf("update lost: %+v", got)
	}
	dup := profileRow("p2")
	dup.ConfigDir = p.ConfigDir // two profiles must never share a directory
	if err := s.PutProfile(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("sharing a config dir = %v, want ErrConflict", err)
	}

	if err := s.CreateEmployee(ctx, employeeRow("e1", "atlas", "p1")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, "p1"); !errors.Is(err, ErrInUse) {
		t.Fatalf("deleting a used profile = %v, want ErrInUse", err)
	}
	if err := s.CreateEmployee(ctx, employeeRow("e2", "bruno", "missing")); !errors.Is(err, ErrInUse) {
		t.Fatalf("an employee needs an existing profile, got %v", err)
	}
	if _, err := s.Profile(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile = %v", err)
	}

	// Once its employees are deleted the profile can go; their history stays.
	if err := s.SetEmployeeState(ctx, "e1", StateDeleted, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, "p1"); err != nil {
		t.Fatalf("a profile used only by deleted employees must be removable: %v", err)
	}
	if e, err := s.Employee(ctx, "e1"); err != nil || e.State != StateDeleted || e.ProfileID != RemovedProfileID {
		t.Fatalf("the deleted employee = %+v, %v; want it kept, pointing at the placeholder", e, err)
	}
	if ps, _ := s.Profiles(ctx); len(ps) != 0 {
		t.Fatalf("the placeholder must never be listed: %+v", ps)
	}
	if _, err := s.Profile(ctx, RemovedProfileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the placeholder is not a profile: %v", err)
	}
	if err := s.DeleteProfile(ctx, RemovedProfileID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the placeholder cannot be deleted: %v", err)
	}
}

func TestEmployeeSlugIsUniqueOnlyAmongLiveEmployees(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	if err := s.PutProfile(ctx, profileRow("p1")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmployee(ctx, employeeRow("e1", "atlas", "p1")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmployee(ctx, employeeRow("e2", "atlas", "p1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("same slug = %v, want ErrConflict", err)
	}
	if err := s.SetEmployeeState(ctx, "e1", StateDeleted, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmployee(ctx, employeeRow("e3", "atlas", "p1")); err != nil {
		t.Fatalf("the slug of a deleted employee must be reusable: %v", err)
	}
	if err := s.SetEmployeeState(ctx, "e1", StateActive, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deleted employee never comes back: %v", err)
	}
	live, _ := s.Employees(ctx, false)
	all, _ := s.Employees(ctx, true)
	if len(live) != 1 || len(all) != 2 {
		t.Fatalf("live %d, all %d; want 1 and 2", len(live), len(all))
	}
	if e, _ := s.Employee(ctx, "e1"); e.DeletedAt.IsZero() {
		t.Fatal("a deleted employee records when")
	}
}

func TestAuditIsAppendOnlyInTheDatabase(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	id, err := s.AppendAudit(ctx, AuditRow{EmployeeID: "ghost", Kind: "permission", Action: "run", Detail: "ls", Decision: "allowed"})
	if err != nil {
		t.Fatalf("an entry about an employee that does not exist must be accepted: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE audit_log SET decision = 'denied' WHERE id = ?`, id); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE = %v, want the database to refuse it", err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM audit_log`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE = %v, want the database to refuse it", err)
	}
	rows, err := s.Audit(ctx, "ghost", 0)
	if err != nil || len(rows) != 1 || rows[0].Decision != "allowed" || rows[0].At.IsZero() {
		t.Fatalf("audit = %+v, %v", rows, err)
	}
}
