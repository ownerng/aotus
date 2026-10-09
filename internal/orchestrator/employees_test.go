package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"aotus/internal/datadir"
	"aotus/internal/provider"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

type env struct {
	svc    *Service
	st     *store.Store
	layout datadir.Layout
	prof   provider.Profile
}

func newEnv(t *testing.T) *env {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	st, err := store.Open(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{svc: New(st, workspace.New(l)), st: st, layout: l}
	e.prof = e.addProfile(t, provider.KindClaude, "Main")
	return e
}

func (e *env) addProfile(t *testing.T, kind provider.Kind, name string) provider.Profile {
	t.Helper()
	p, err := provider.NewProfile(e.layout, kind, name, "/usr/bin/"+string(kind))
	if err != nil {
		t.Fatal(err)
	}
	p.Mode = provider.ModeStructured
	if err := e.svc.AddProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) create(t *testing.T, name string) Employee {
	t.Helper()
	emp, err := e.svc.CreateEmployee(context.Background(), NewEmployee{Name: name, Role: "tester", ProfileID: e.prof.ID})
	if err != nil {
		t.Fatalf("CreateEmployee(%q): %v", name, err)
	}
	return emp
}

func TestCreateEmployeeValidatesName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	long := strings.Repeat("x", maxName+1)
	for name, in := range map[string]NewEmployee{
		"empty name":        {Name: "", ProfileID: e.prof.ID},
		"blank name":        {Name: "   ", ProfileID: e.prof.ID},
		"no usable letters": {Name: "!!!", ProfileID: e.prof.ID},
		"reserved name":     {Name: "CON", ProfileID: e.prof.ID},
		"too long name":     {Name: long, ProfileID: e.prof.ID},
		"too long role":     {Name: "A", Role: strings.Repeat("r", maxRole+1), ProfileID: e.prof.ID},
		"too long prompt":   {Name: "A", SystemPrompt: strings.Repeat("p", maxPrompt+1), ProfileID: e.prof.ID},
		"no profile chosen": {Name: "A"},
	} {
		if _, err := e.svc.CreateEmployee(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if emps, _ := e.svc.Employees(ctx); len(emps) != 0 {
		t.Fatalf("rejected employees must leave nothing behind: %v", emps)
	}

	atlas := e.create(t, "  Atlas  ")
	if atlas.Name != "Atlas" || atlas.Slug != "atlas" || atlas.State != StateActive {
		t.Fatalf("employee = %+v", atlas)
	}
	// The same name, another case, or accents that fold to the same folder.
	for _, dup := range []string{"Atlas", "ATLAS", "átlas", "atlas!"} {
		if _, err := e.svc.CreateEmployee(ctx, NewEmployee{Name: dup, ProfileID: e.prof.ID}); !errors.Is(err, ErrNameTaken) {
			t.Errorf("%q: err = %v, want ErrNameTaken", dup, err)
		}
	}
	if _, err := e.svc.Employee(ctx, atlas.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEmployeeBoundToOneProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.svc.CreateEmployee(ctx, NewEmployee{Name: "Ghost", ProfileID: "claude-nope-0000"}); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("an unknown profile must be refused, got %v", err)
	}
	if _, err := os.Stat(e.layout.EmployeeDir("ghost")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused employee must not leave a folder")
	}

	codex := e.addProfile(t, provider.KindCodex, "Second")
	a := e.create(t, "Atlas")
	b, err := e.svc.CreateEmployee(ctx, NewEmployee{Name: "Bruno", ProfileID: codex.ID})
	if err != nil {
		t.Fatal(err)
	}
	if a.ProfileID != e.prof.ID || b.ProfileID != codex.ID {
		t.Fatalf("each employee keeps the profile it was created with: %q %q", a.ProfileID, b.ProfileID)
	}
	if got, _ := e.svc.Profile(ctx, a.ProfileID); got.ConfigDir == mustProfile(t, e, b.ProfileID).ConfigDir {
		t.Fatal("two profiles must not share a configuration directory")
	}

	// A profile that employees use cannot be removed; an unused one can.
	if err := e.svc.RemoveProfile(ctx, e.prof.ID); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("removing a used profile = %v, want ErrProfileInUse", err)
	}
	unused := e.addProfile(t, provider.KindClaude, "Unused")
	if err := e.svc.RemoveProfile(ctx, unused.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveProfile(ctx, unused.ID); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("removing twice = %v, want ErrNoProfile", err)
	}

	// A profile that points at the CLI's own default location is refused.
	home, _ := os.UserHomeDir()
	bad := e.prof
	bad.ID = "claude-shared-login-0001"
	bad.ConfigDir = filepath.Join(home, ".claude")
	if err := e.svc.AddProfile(ctx, bad); err == nil {
		t.Fatal("a profile sharing the user's own login must be refused")
	}
}

func mustProfile(t *testing.T, e *env, id string) provider.Profile {
	t.Helper()
	p, err := e.svc.Profile(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPauseAndResumeSurviveRestart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.create(t, "Atlas")
	b := e.create(t, "Bruno")
	if err := e.svc.Pause(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	// Restart: close everything and open it again from disk.
	if err := e.st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(ctx, e.layout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	svc2 := New(st2, workspace.New(e.layout))

	got, err := svc2.Employee(ctx, a.ID)
	if err != nil || got.State != StatePaused {
		t.Fatalf("after a restart the paused employee is %+v, %v", got, err)
	}
	if got, _ := svc2.Employee(ctx, b.ID); got.State != StateActive {
		t.Fatalf("the other employee must stay active, got %s", got.State)
	}
	if err := svc2.Resume(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc2.Employee(ctx, a.ID); got.State != StateActive {
		t.Fatalf("resumed employee is %s", got.State)
	}
	if err := svc2.Pause(ctx, "emp-doesnotexist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pausing an unknown employee = %v, want ErrNotFound", err)
	}
}

func TestDeleteKeepsAuditLog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.create(t, "Atlas")
	folder := e.layout.EmployeeDir(a.Slug)
	if err := os.WriteFile(filepath.Join(folder, "report.md"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pause(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.DeleteEmployee(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Employee(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deleted employee is gone from the API: %v", err)
	}
	if emps, _ := e.svc.Employees(ctx); len(emps) != 0 {
		t.Fatalf("a deleted employee must not be listed: %v", emps)
	}
	if err := e.svc.DeleteEmployee(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice = %v, want ErrNotFound", err)
	}
	if err := e.svc.Resume(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deleted employee cannot be resumed: %v", err)
	}

	// The folder went to the trash, files intact.
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder must leave employees/: %v", err)
	}
	trash, _ := filepath.Glob(filepath.Join(e.layout.TrashDir(), "atlas-*", "report.md"))
	if len(trash) != 1 {
		t.Fatalf("the files must be in the trash, found %v", trash)
	}

	// The audit log outlived the employee and tells the whole story.
	log, err := e.svc.Audit(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, row := range log {
		actions = append(actions, row.Action)
	}
	if got := strings.Join(actions, ","); got != "created,paused,deleted" {
		t.Fatalf("audit actions = %s, want created,paused,deleted", got)
	}
	if !strings.Contains(log[2].Detail, "trash") {
		t.Errorf("the deletion entry should say where the folder went: %q", log[2].Detail)
	}

	// The name can be used again, with a fresh folder.
	again := e.create(t, "Atlas")
	if again.ID == a.ID {
		t.Fatal("a new employee gets a new ID")
	}
	if entries, _ := os.ReadDir(e.layout.EmployeeDir("atlas")); len(entries) != 1 || entries[0].Name() != ".tmp" {
		t.Fatalf("the new folder must start empty, got %v", entries)
	}
}

func TestWorkingFolderStaysInsideDataDir(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, name := range []string{"Atlas", "../../etc/passwd", "..", "a/b/c", `C:\Windows`, "~root"} {
		emp, err := e.svc.CreateEmployee(ctx, NewEmployee{Name: name, ProfileID: e.prof.ID})
		if err != nil {
			continue // refused names are fine; the point is where accepted ones land
		}
		folder := e.layout.EmployeeDir(emp.Slug)
		rel, err := filepath.Rel(e.layout.EmployeesDir(), folder)
		if err != nil || strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
			t.Errorf("name %q gave the folder %s, which is not a direct child of employees/", name, folder)
		}
		if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
			t.Errorf("name %q: the folder was not created: %v", name, err)
		}
	}
	// Whatever happened, nothing was written outside the data directory.
	parent := filepath.Dir(e.layout.Root)
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != "aotus" {
		t.Fatalf("files appeared next to the data directory: %v", entries)
	}
}

func TestConcurrentCreateOfTheSameNameHasOneWinner(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.CreateEmployee(context.Background(), NewEmployee{Name: "Atlas", ProfileID: e.prof.ID})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var ok, taken int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrNameTaken):
			taken++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || taken != 7 {
		t.Fatalf("%d created and %d refused, want exactly 1 and 7", ok, taken)
	}
}

func TestProfileStoredWithoutSecrets(t *testing.T) {
	e := newEnv(t)
	p, _ := provider.NewProfile(e.layout, provider.KindOpenAI, "api", "")
	p.Mode, p.Model, p.APIKeyRef = provider.ModeAPI, "gpt-x", "profile/"+p.ID+"/api-key"
	p.AcceptedNotices = []string{"n1"}
	p.ExtraEnv = map[string]string{"HTTPS_PROXY": "http://proxy:3128"}
	ctx := context.Background()
	if err := e.svc.AddProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	got := mustProfile(t, e, p.ID)
	if got.APIKeyRef != p.APIKeyRef || got.Model != "gpt-x" || got.AcceptedNotices[0] != "n1" || got.ExtraEnv["HTTPS_PROXY"] == "" {
		t.Fatalf("profile did not round-trip: %+v", got)
	}
	leaky := p
	leaky.ID, leaky.ConfigDir = "openai-api-leaky-0001", e.layout.ProfileDir("openai-api-leaky-0001")
	leaky.ExtraEnv = map[string]string{"OPENAI_API_KEY": "sk-secret"}
	if err := e.svc.AddProfile(ctx, leaky); err == nil {
		t.Fatal("a profile carrying a secret in its environment must be refused")
	}
}
