package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoRoot is the repository root, two levels above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func okCheck() []Check { return []Check{{Name: "c", File: "x.md"}} }

// fixture is a small valid model: phase 0 with two chained tasks, phase 1
// planned with one task, phase 2 unplanned.
func fixture() *Model {
	return &Model{
		Root: "",
		Phases: []Phase{
			{ID: 0, Name: "Zero", Goal: "g", Planned: true, Gate: []string{"gate"}},
			{ID: 1, Name: "One", Goal: "g", Planned: true, Gate: []string{"gate"}},
			{ID: 2, Name: "Two", Goal: "g"},
		},
		Requirements: []Requirement{{ID: "F1", Title: "t", Phase: 0, Kind: "functional"}},
		Tasks: []*Task{
			{ID: "P0-001", Phase: 0, Title: "a", Status: Todo, Description: "d", Acceptance: []string{"x"}, Checks: okCheck(), Requirements: []string{"F1"}},
			{ID: "P0-002", Phase: 0, Title: "b", Status: Todo, Description: "d", Acceptance: []string{"x"}, Checks: okCheck(), DependsOn: []string{"P0-001"}},
			{ID: "P1-001", Phase: 1, Title: "c", Status: Todo, Description: "d", Acceptance: []string{"x"}, Checks: okCheck()},
		},
		taskFile: map[string]string{},
	}
}

func messages(issues []Issue) string {
	var b strings.Builder
	for _, i := range issues {
		b.WriteString(i.String() + "\n")
	}
	return b.String()
}

func TestLintAcceptsRepositoryHarness(t *testing.T) {
	m, err := Load(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	issues := Lint(m)
	if HasErrors(issues) {
		t.Fatalf("the repository harness must lint clean:\n%s", messages(issues))
	}
}

func TestLintCatchesDependencyCycle(t *testing.T) {
	m := fixture()
	m.Task("P0-001").DependsOn = []string{"P0-002"}
	got := messages(Lint(m))
	if !strings.Contains(got, "cycle") {
		t.Fatalf("expected a dependency cycle error, got:\n%s", got)
	}
}

func TestLintCatchesBrokenReferences(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Model)
		want   string
	}{
		{"unknown dependency", func(m *Model) { m.Task("P0-002").DependsOn = []string{"P0-099"} }, "unknown dependency"},
		{"unknown requirement", func(m *Model) { m.Task("P0-001").Requirements = []string{"Z9"} }, "unknown requirement"},
		{"duplicate id", func(m *Model) { m.Tasks = append(m.Tasks, &Task{ID: "P0-001", Phase: 0}) }, "duplicate id"},
		{"phase mismatch", func(m *Model) { m.Task("P1-001").Phase = 0 }, "id prefix says phase 1"},
		{"no acceptance", func(m *Model) { m.Task("P0-001").Acceptance = nil }, "acceptance"},
		{"no checks", func(m *Model) { m.Task("P0-001").Checks = nil }, "check"},
		{"two check kinds", func(m *Model) {
			m.Task("P0-001").Checks = []Check{{Name: "c", File: "x", Cmd: []string{"go"}}}
		}, "exactly one"},
		{"done without date", func(m *Model) { m.Task("P0-001").Status = Done }, "completed_at"},
		{"later phase dependency", func(m *Model) { m.Task("P0-001").DependsOn = []string{"P1-001"} }, "later phase"},
		{"planned phase without tasks", func(m *Model) { m.Phases[2].Planned = true; m.Phases[2].Gate = []string{"g"} }, "no tasks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			tc.mutate(m)
			if got := messages(Lint(m)); !strings.Contains(got, tc.want) {
				t.Fatalf("want an issue containing %q, got:\n%s", tc.want, got)
			}
		})
	}
}

func TestLintEnforcesWorkflowInvariants(t *testing.T) {
	t.Run("work in a later phase", func(t *testing.T) {
		m := fixture()
		m.Task("P1-001").Status = InProgress
		if got := messages(Lint(m)); !strings.Contains(got, "not finished") {
			t.Fatalf("got:\n%s", got)
		}
	})
	t.Run("two tasks in progress", func(t *testing.T) {
		m := fixture()
		m.Task("P0-001").Status = InProgress
		m.Task("P0-002").Status = InProgress
		if got := messages(Lint(m)); !strings.Contains(got, "in_progress; the limit") {
			t.Fatalf("got:\n%s", got)
		}
	})
	t.Run("in progress before its dependency is done", func(t *testing.T) {
		m := fixture()
		m.Task("P0-002").Status = InProgress
		if got := messages(Lint(m)); !strings.Contains(got, "dependency P0-001") {
			t.Fatalf("got:\n%s", got)
		}
	})
}

func TestStartEnforcesWorkflow(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

	m := fixture()
	if err := m.Start("P0-002"); err == nil || !strings.Contains(err.Error(), "not done") {
		t.Fatalf("starting a task with an unfinished dependency must fail, got %v", err)
	}
	if err := m.Start("P1-001"); err == nil || !strings.Contains(err.Error(), "current phase") {
		t.Fatalf("starting a task of a later phase must fail, got %v", err)
	}
	if err := m.Start("P0-001"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("P0-002"); err == nil {
		t.Fatal("a second task must not start while one is in progress")
	}
	if err := m.Complete("P0-002", now); err == nil {
		t.Fatal("only in-progress tasks can be completed")
	}
	if err := m.Complete("P0-001", now); err != nil {
		t.Fatal(err)
	}
	if got := m.Task("P0-001"); got.Status != Done || got.CompletedAt != "2026-10-08" {
		t.Fatalf("task not completed correctly: %+v", got)
	}
	if err := m.Start("P0-002"); err != nil {
		t.Fatalf("dependency is done, start must work: %v", err)
	}
	if err := m.Block("P0-002", " "); err == nil {
		t.Fatal("blocking requires a reason")
	}
	if err := m.Block("P0-002", "waiting for terms"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("P0-002"); err != nil {
		t.Fatalf("a blocked task can be restarted: %v", err)
	}
}

func TestLintAllowsDocsProducedByEarlierTasks(t *testing.T) {
	m := fixture()
	m.Task("P1-001").Docs = []string{"docs/not-written-yet.md"}
	if got := messages(Lint(m)); !strings.Contains(got, "no earlier task produces it") {
		t.Fatalf("a missing doc nobody produces must be an error, got:\n%s", got)
	}
	m.Task("P0-001").Paths = []string{"docs/not-written-yet.md"}
	if got := messages(Lint(m)); strings.Contains(got, "not-written-yet") {
		t.Fatalf("a doc produced by an earlier task must be accepted, got:\n%s", got)
	}
}

func TestEligibleHonorsDependenciesAndPhase(t *testing.T) {
	m := fixture()
	if got := ids(m.Eligible()); got != "P0-001" {
		t.Fatalf("only the task without pending dependencies is eligible, got %s", got)
	}
	m.Task("P0-001").Status = Done
	if got := ids(m.Eligible()); got != "P0-002" {
		t.Fatalf("got %s", got)
	}
}

func ids(ts []*Task) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.ID)
	}
	return strings.Join(s, ",")
}

func TestGateClosesPhaseAndAdvances(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	m := fixture()
	if err := m.PassGate(0, now); err == nil {
		t.Fatal("a phase with open tasks cannot pass its gate")
	}
	for _, id := range []string{"P0-001", "P0-002"} {
		m.Task(id).Status, m.Task(id).CompletedAt = Done, "2026-10-08"
	}
	if err := m.PassGate(1, now); err == nil {
		t.Fatal("only the current phase can pass its gate")
	}
	if err := m.PassGate(0, now); err != nil {
		t.Fatal(err)
	}
	if cur := m.CurrentPhase(); cur == nil || cur.ID != 1 {
		t.Fatalf("current phase should advance to 1, got %+v", cur)
	}
}

func TestSummaryAndRenderingAreConsistent(t *testing.T) {
	m := fixture()
	m.Task("P0-001").Status, m.Task("P0-001").CompletedAt = Done, "2026-10-08"
	s := Summarize(m)
	if s.TotalTasks != 3 || s.DoneTasks != 1 {
		t.Fatalf("totals wrong: %+v", s)
	}
	if s.CurrentPhase == nil || *s.CurrentPhase != 0 {
		t.Fatalf("current phase wrong: %+v", s.CurrentPhase)
	}
	if len(s.Next) != 1 || s.Next[0].ID != "P0-002" {
		t.Fatalf("next wrong: %+v", s.Next)
	}
	text := RenderText(m)
	for _, want := range []string{"1/3 tasks done", "Current phase: 0", "P0-002"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text status misses %q:\n%s", want, text)
		}
	}
	first, second := RenderMarkdown(m), RenderMarkdown(m)
	if first != second {
		t.Fatal("markdown must be deterministic")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"harness/tasks", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(PhasesFile, `[{"id":0,"name":"Z","goal":"g","planned":true,"gate":["x"]}]`)
	write(RequirementsFile, `[]`)
	write("harness/tasks/phase-0.json", `[{"id":"P0-001","phase":0,"title":"t","status":"todo","description":"d","acceptance":["a"],"checks":[{"name":"c","file":"x"}]}]`)

	m, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start("P0-001"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if again.Task("P0-001").Status != InProgress {
		t.Fatal("status was not persisted")
	}

	write("harness/tasks/phase-0.json", `[{"id":"P0-001","phase":0,"titel":"typo"}]`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown JSON fields must be rejected, got %v", err)
	}
}

func TestFileCheckContains(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("Decision: use Go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.md"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		check Check
		want  Outcome
	}{
		{"present", Check{Name: "x", File: "a.md", Contains: []string{"Decision:"}}, Pass},
		{"missing text", Check{Name: "x", File: "a.md", Contains: []string{"Nope"}}, Fail},
		{"missing file", Check{Name: "x", File: "b.md"}, Fail},
		{"empty file", Check{Name: "x", File: "empty.md"}, Fail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RunCheck(root, tc.check).Outcome; got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestGoTestsCheckRequiresNamedTests(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module sample\n\ngo 1.24\n",
		"a.go":      "package sample\n",
		"a_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\n\nfunc TestFails(t *testing.T) { t.Fatal(\"boom\") }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		tests []string
		want  Outcome
	}{
		{"existing passing test", []string{"TestOne"}, Pass},
		{"missing test must fail even though go test would pass", []string{"TestDoesNotExist"}, Fail},
		{"one of several missing", []string{"TestOne", "TestDoesNotExist"}, Fail},
		{"failing test", []string{"TestFails"}, Fail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RunCheck(root, Check{Name: "t", Pkg: ".", Tests: tc.tests})
			if got.Outcome != tc.want {
				t.Fatalf("got %s want %s\n%s", got.Outcome, tc.want, got.Output)
			}
		})
	}
}

func TestCheckArchReportsViolations(t *testing.T) {
	rules := &ArchRules{
		Layers: []Layer{
			{Package: "internal/store", MayImport: nil},
			{Package: "internal/api", MayImport: []string{"internal/store"}},
			{Package: "internal/provider", MayImport: nil},
			{Package: "cmd", MayImport: []string{"*"}},
		},
		Restricted: []Restriction{
			{Import: "os/exec", AllowedIn: []string{"internal/provider"}},
			{Import: "C", AllowedIn: nil},
		},
	}
	const mod = "m"
	cases := []struct {
		name string
		pkg  Pkg
		want string // substring of the expected violation, empty for none
	}{
		{"allowed import", Pkg{ImportPath: "m/internal/api", Imports: []string{"m/internal/store", "fmt"}}, ""},
		{"sub-package inherits its layer", Pkg{ImportPath: "m/internal/api/web", Imports: []string{"m/internal/store"}}, ""},
		{"forbidden internal import", Pkg{ImportPath: "m/internal/store", Imports: []string{"m/internal/api"}}, "does not allow"},
		{"wildcard layer", Pkg{ImportPath: "m/cmd/daemon", Imports: []string{"m/internal/api", "m/internal/store"}}, ""},
		{"uncovered package", Pkg{ImportPath: "m/internal/newthing"}, "not covered by any layer"},
		{"os/exec outside allowed packages", Pkg{ImportPath: "m/internal/api", Imports: []string{"os/exec"}}, "only allowed in"},
		{"os/exec in allowed package", Pkg{ImportPath: "m/internal/provider", Imports: []string{"os/exec"}}, ""},
		{"cgo", Pkg{ImportPath: "m/internal/store", Imports: []string{"C"}}, "only allowed in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(CheckArch(rules, mod, []Pkg{tc.pkg}), "\n")
			if tc.want == "" && got != "" {
				t.Fatalf("unexpected violation: %s", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("want violation containing %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRepositoryArchitectureHolds(t *testing.T) {
	v, err := Arch(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(v) > 0 {
		t.Fatalf("architecture violations:\n%s", strings.Join(v, "\n"))
	}
}
