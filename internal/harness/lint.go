package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Level is the severity of a lint issue.
type Level string

// Issue severities.
const (
	LevelError Level = "error"
	LevelWarn  Level = "warn"
)

// Issue is one problem found by Lint.
type Issue struct {
	Level Level
	Msg   string
}

func (i Issue) String() string { return fmt.Sprintf("%s: %s", i.Level, i.Msg) }

// HasErrors reports whether any issue is an error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Level == LevelError {
			return true
		}
	}
	return false
}

var taskIDRe = regexp.MustCompile(`^P(\d+)-(\d{3})$`)

// MaxInProgress is the work-in-progress limit: one task at a time keeps every
// model (and human) focused and makes the status report unambiguous.
const MaxInProgress = 1

// Lint validates the whole harness model: schema, references, dependency
// graph, workflow invariants and the freshness of docs/STATUS.md.
func Lint(m *Model) []Issue {
	l := &linter{m: m}
	l.phases()
	l.requirements()
	l.tasks()
	l.graph()
	l.workflow()
	l.coverage()
	l.statusFresh()
	return l.issues
}

type linter struct {
	m      *Model
	issues []Issue
}

func (l *linter) errf(format string, a ...any) {
	l.issues = append(l.issues, Issue{LevelError, fmt.Sprintf(format, a...)})
}

func (l *linter) phases() {
	seen := map[int]bool{}
	for _, p := range l.m.Phases {
		if seen[p.ID] {
			l.errf("phase %d: duplicate id", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" || p.Goal == "" {
			l.errf("phase %d: name and goal are required", p.ID)
		}
		if p.Planned && len(p.Gate) == 0 {
			l.errf("phase %d: planned phases need at least one gate criterion", p.ID)
		}
		for _, c := range p.GateChecks {
			l.check(fmt.Sprintf("phase %d gate", p.ID), c)
		}
		n := len(l.m.TasksOf(p.ID))
		switch {
		case p.Planned && n == 0:
			l.errf("phase %d is marked planned but has no tasks", p.ID)
		case !p.Planned && n > 0:
			l.errf("phase %d has tasks but is not marked planned", p.ID)
		}
	}
	if len(l.m.Phases) == 0 {
		l.errf("%s: no phases defined", PhasesFile)
	}
}

func (l *linter) requirements() {
	seen := map[string]bool{}
	for _, r := range l.m.Requirements {
		if r.ID == "" || r.Title == "" {
			l.errf("requirement %q: id and title are required", r.ID)
		}
		if seen[r.ID] {
			l.errf("requirement %s: duplicate id", r.ID)
		}
		seen[r.ID] = true
		if l.m.Phase(r.Phase) == nil {
			l.errf("requirement %s: unknown phase %d", r.ID, r.Phase)
		}
		if r.Kind != "functional" && r.Kind != "nonfunctional" {
			l.errf("requirement %s: kind must be functional or nonfunctional", r.ID)
		}
	}
}

func (l *linter) check(owner string, c Check) {
	if c.Name == "" {
		l.errf("%s: every check needs a name", owner)
	}
	hasCmd, hasFile, hasTests := len(c.Cmd) > 0, c.File != "", c.Pkg != "" || len(c.Tests) > 0
	kinds := 0
	for _, b := range []bool{hasCmd, hasFile, hasTests} {
		if b {
			kinds++
		}
	}
	if kinds != 1 {
		l.errf("%s: check %q must set exactly one of cmd, file or pkg+tests", owner, c.Name)
	}
	if len(c.Tags) > 0 && c.Pkg == "" {
		l.errf("%s: check %q uses tags without pkg", owner, c.Name)
	}
	if hasTests && (c.Pkg == "" || len(c.Tests) == 0) {
		l.errf("%s: check %q needs both pkg and tests", owner, c.Name)
	}
	if hasCmd && c.Cmd[0] == "" {
		l.errf("%s: check %q has an empty command", owner, c.Name)
	}
	if len(c.Contains) > 0 && !hasFile {
		l.errf("%s: check %q uses contains without file", owner, c.Name)
	}
}

func (l *linter) tasks() {
	seen := map[string]bool{}
	for _, t := range l.m.Tasks {
		if seen[t.ID] {
			l.errf("task %s: duplicate id", t.ID)
		}
		seen[t.ID] = true

		sub := taskIDRe.FindStringSubmatch(t.ID)
		if sub == nil {
			l.errf("task %s: id must look like P1-001", t.ID)
		} else if n, _ := strconv.Atoi(sub[1]); n != t.Phase {
			l.errf("task %s: id prefix says phase %d but phase field is %d", t.ID, n, t.Phase)
		}
		if l.m.Phase(t.Phase) == nil {
			l.errf("task %s: unknown phase %d", t.ID, t.Phase)
		}
		if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Description) == "" {
			l.errf("task %s: title and description are required", t.ID)
		}
		if !t.Status.valid() {
			l.errf("task %s: invalid status %q", t.ID, t.Status)
		}
		if len(t.Acceptance) == 0 {
			l.errf("task %s: needs at least one acceptance criterion", t.ID)
		}
		if len(t.Checks) == 0 {
			l.errf("task %s: needs at least one machine-verifiable check", t.ID)
		}
		for _, c := range t.Checks {
			l.check("task "+t.ID, c)
		}
		for _, r := range t.Requirements {
			if l.m.Requirement(r) == nil {
				l.errf("task %s: unknown requirement %s", t.ID, r)
			}
		}
		for _, d := range t.Docs {
			if _, err := os.Stat(filepath.Join(l.m.Root, filepath.FromSlash(d))); err != nil && !l.producedEarlier(d, t) {
				l.errf("task %s: doc %s does not exist and no earlier task produces it", t.ID, d)
			}
		}
		switch {
		case t.Status == Done && t.CompletedAt == "":
			l.errf("task %s: done tasks need completed_at", t.ID)
		case t.Status != Done && t.CompletedAt != "":
			l.errf("task %s: completed_at is set but status is %s", t.ID, t.Status)
		}
	}
}

// producedEarlier reports whether another task from the same or an earlier
// phase lists path among the files it will write, so a task may ask to read a
// document that an earlier task creates.
func (l *linter) producedEarlier(path string, reader *Task) bool {
	for _, t := range l.m.Tasks {
		if t.ID == reader.ID || t.Phase > reader.Phase {
			continue
		}
		for _, p := range t.Paths {
			if p == path {
				return true
			}
		}
	}
	return false
}

// graph checks dependency references, direction and cycles.
func (l *linter) graph() {
	for _, t := range l.m.Tasks {
		for _, d := range t.DependsOn {
			dep := l.m.Task(d)
			switch {
			case d == t.ID:
				l.errf("task %s: depends on itself", t.ID)
			case dep == nil:
				l.errf("task %s: unknown dependency %s", t.ID, d)
			case dep.Phase > t.Phase:
				l.errf("task %s: depends on %s from a later phase", t.ID, d)
			}
		}
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		color[id] = grey
		if t := l.m.Task(id); t != nil {
			for _, d := range t.DependsOn {
				switch color[d] {
				case grey:
					l.errf("dependency cycle: %s -> %s", strings.Join(append(path, id), " -> "), d)
				case white:
					visit(d, append(path, id))
				}
			}
		}
		color[id] = black
	}
	for _, t := range l.m.Tasks {
		if color[t.ID] == white {
			visit(t.ID, nil)
		}
	}
}

// workflow checks the invariants the CLI itself enforces, so hand edits that
// bypass the CLI are caught.
func (l *linter) workflow() {
	cur := l.m.CurrentPhase()
	inProgress := 0
	for _, t := range l.m.Tasks {
		if t.Status == InProgress {
			inProgress++
		}
		if t.Status == Todo {
			continue
		}
		if cur != nil && t.Phase > cur.ID {
			l.errf("task %s is %s but phase %d is not finished (current phase is %d)", t.ID, t.Status, t.Phase, cur.ID)
		}
		if t.Status == InProgress || t.Status == Done {
			for _, d := range t.DependsOn {
				if dep := l.m.Task(d); dep != nil && dep.Status != Done {
					l.errf("task %s is %s but dependency %s is %s", t.ID, t.Status, d, dep.Status)
				}
			}
		}
	}
	if inProgress > MaxInProgress {
		l.errf("%d tasks are in_progress; the limit is %d", inProgress, MaxInProgress)
	}
	for i, p := range l.m.Phases {
		if p.GatePassedAt == "" {
			continue
		}
		if i > 0 && l.m.Phases[i-1].GatePassedAt == "" {
			l.errf("phase %d gate is passed but phase %d gate is not", p.ID, l.m.Phases[i-1].ID)
		}
		for _, t := range l.m.TasksOf(p.ID) {
			if t.Status != Done {
				l.errf("phase %d gate is passed but task %s is %s", p.ID, t.ID, t.Status)
			}
		}
	}
}

// coverage makes sure every requirement of a planned phase has a task.
func (l *linter) coverage() {
	for _, r := range l.m.Requirements {
		p := l.m.Phase(r.Phase)
		if p == nil || !p.Planned {
			continue
		}
		covered := false
		for _, t := range l.m.Tasks {
			for _, id := range t.Requirements {
				if id == r.ID {
					covered = true
				}
			}
		}
		if !covered {
			l.errf("requirement %s (phase %d) has no task", r.ID, r.Phase)
		}
	}
}

func (l *linter) statusFresh() {
	path := filepath.Join(l.m.Root, filepath.FromSlash(StatusFile))
	got, err := os.ReadFile(path) //nolint:gosec // fixed path under the repo root
	if err != nil {
		l.errf("%s is missing: run `go run ./cmd/crew report`", StatusFile)
		return
	}
	if string(got) != RenderMarkdown(l.m) {
		l.errf("%s is stale: run `go run ./cmd/crew report`", StatusFile)
	}
}
