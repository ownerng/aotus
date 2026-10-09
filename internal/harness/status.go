package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CurrentPhase is the first phase whose exit gate has not been passed, or nil
// when the whole roadmap is complete.
func (m *Model) CurrentPhase() *Phase {
	for i := range m.Phases {
		if m.Phases[i].GatePassedAt == "" {
			return &m.Phases[i]
		}
	}
	return nil
}

// Eligible returns the tasks that can be started right now: todo, in the
// current phase, with every dependency done.
func (m *Model) Eligible() []*Task {
	cur := m.CurrentPhase()
	if cur == nil {
		return nil
	}
	var out []*Task
	for _, t := range m.TasksOf(cur.ID) {
		if t.Status == Todo && m.depsDone(t) {
			out = append(out, t)
		}
	}
	return out
}

func (m *Model) depsDone(t *Task) bool {
	for _, d := range t.DependsOn {
		if dep := m.Task(d); dep == nil || dep.Status != Done {
			return false
		}
	}
	return true
}

// PhaseProgress summarises one phase.
type PhaseProgress struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Planned      bool   `json:"planned"`
	Total        int    `json:"total"`
	Done         int    `json:"done"`
	InProgress   int    `json:"in_progress"`
	Blocked      int    `json:"blocked"`
	GatePassed   bool   `json:"gate_passed"`
	ReadyForGate bool   `json:"ready_for_gate"`
}

// Percent is the share of finished tasks, 0 when the phase has none.
func (p PhaseProgress) Percent() int {
	if p.Total == 0 {
		return 0
	}
	return p.Done * 100 / p.Total
}

// TaskRef is the short form of a task used in summaries.
type TaskRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
}

// Summary is the machine-readable project status (crew status --json).
type Summary struct {
	CurrentPhase *int            `json:"current_phase"`
	Phases       []PhaseProgress `json:"phases"`
	InProgress   []TaskRef       `json:"in_progress"`
	Blocked      []TaskRef       `json:"blocked"`
	Next         []TaskRef       `json:"next"`
	TotalTasks   int             `json:"total_tasks"`
	DoneTasks    int             `json:"done_tasks"`
}

func ref(t *Task) TaskRef { return TaskRef{t.ID, t.Title, t.Status} }

// Summarize computes the current status of the project.
func Summarize(m *Model) Summary {
	s := Summary{
		Phases:     []PhaseProgress{},
		InProgress: []TaskRef{},
		Blocked:    []TaskRef{},
		Next:       []TaskRef{},
	}
	if cur := m.CurrentPhase(); cur != nil {
		id := cur.ID
		s.CurrentPhase = &id
	}
	for _, p := range m.Phases {
		pp := PhaseProgress{ID: p.ID, Name: p.Name, Planned: p.Planned, GatePassed: p.GatePassedAt != ""}
		for _, t := range m.TasksOf(p.ID) {
			pp.Total++
			switch t.Status {
			case Done:
				pp.Done++
			case InProgress:
				pp.InProgress++
			case Blocked:
				pp.Blocked++
			}
		}
		pp.ReadyForGate = p.Planned && pp.Total > 0 && pp.Done == pp.Total && !pp.GatePassed
		s.TotalTasks += pp.Total
		s.DoneTasks += pp.Done
		s.Phases = append(s.Phases, pp)
	}
	for _, t := range m.Tasks {
		switch t.Status {
		case InProgress:
			s.InProgress = append(s.InProgress, ref(t))
		case Blocked:
			s.Blocked = append(s.Blocked, ref(t))
		}
	}
	for _, t := range m.Eligible() {
		s.Next = append(s.Next, ref(t))
	}
	return s
}

func (s Summary) phase(id int) PhaseProgress {
	for _, p := range s.Phases {
		if p.ID == id {
			return p
		}
	}
	return PhaseProgress{}
}

func phaseLabel(p PhaseProgress) string {
	switch {
	case !p.Planned:
		return "not planned yet"
	case p.GatePassed:
		return "gate passed"
	case p.ReadyForGate:
		return "ready for gate: run `crew gate " + fmt.Sprint(p.ID) + "`"
	default:
		return fmt.Sprintf("%d/%d tasks (%d%%)", p.Done, p.Total, p.Percent())
	}
}

// RenderText is the compact status shown by `crew status`.
func RenderText(m *Model) string {
	s := Summarize(m)
	var b strings.Builder
	fmt.Fprintf(&b, "Overall: %d/%d tasks done\n", s.DoneTasks, s.TotalTasks)
	if s.CurrentPhase == nil {
		b.WriteString("Current phase: none, the roadmap is complete\n")
	} else {
		p := m.Phase(*s.CurrentPhase)
		fmt.Fprintf(&b, "Current phase: %d - %s (%s)\n", p.ID, p.Name, phaseLabel(s.phase(p.ID)))
		fmt.Fprintf(&b, "Goal: %s\n", p.Goal)
		if !p.Planned {
			b.WriteString("This phase has no tasks yet: plan it (add harness/tasks/phase-N.json) before building.\n")
		}
	}
	section := func(title string, refs []TaskRef) {
		if len(refs) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n", title)
		for _, r := range refs {
			fmt.Fprintf(&b, "  %s  %s\n", r.ID, r.Title)
		}
	}
	section("In progress", s.InProgress)
	section("Blocked", s.Blocked)
	section("Next up (all dependencies done)", s.Next)
	b.WriteString("\nPhases:\n")
	for _, p := range s.Phases {
		mark := " "
		if p.GatePassed {
			mark = "x"
		}
		fmt.Fprintf(&b, "  [%s] %d  %-24s %s\n", mark, p.ID, p.Name, phaseLabel(p))
	}
	return b.String()
}

// RenderBrief prints everything a model needs to start a task.
func RenderBrief(m *Model, t *Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK %s  [%s]  phase %d\n%s\n\n%s\n", t.ID, t.Status, t.Phase, t.Title, t.Description)
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n", title)
		for _, i := range items {
			fmt.Fprintf(&b, "  - %s\n", i)
		}
	}
	list("Acceptance criteria", t.Acceptance)
	var checks []string
	for _, c := range t.Checks {
		switch {
		case len(c.Cmd) > 0:
			checks = append(checks, fmt.Sprintf("%s: %s", c.Name, strings.Join(c.Cmd, " ")))
		case c.Pkg != "":
			checks = append(checks, fmt.Sprintf("%s: in %s these tests must exist and pass: %s", c.Name, c.Pkg, strings.Join(c.Tests, ", ")))
		default:
			s := fmt.Sprintf("%s: file %s must exist", c.Name, c.File)
			if len(c.Contains) > 0 {
				s += " and contain " + strings.Join(c.Contains, ", ")
			}
			checks = append(checks, s)
		}
	}
	list("Checks that will be run by `crew done`", checks)
	var reqs []string
	for _, id := range t.Requirements {
		if r := m.Requirement(id); r != nil {
			reqs = append(reqs, fmt.Sprintf("%s %s", r.ID, r.Title))
		}
	}
	list("Requirements", reqs)
	var docs []string
	for _, d := range t.Docs {
		if _, err := os.Stat(filepath.Join(m.Root, filepath.FromSlash(d))); err != nil {
			d += " (not written yet: produced by an earlier task)"
		}
		docs = append(docs, d)
	}
	list("Read first", docs)
	list("Expected to touch", t.Paths)
	list("Depends on", t.DependsOn)
	if t.Notes != "" {
		fmt.Fprintf(&b, "\nNotes: %s\n", t.Notes)
	}
	fmt.Fprintf(&b, "\nWorkflow: crew start %s -> implement with tests -> crew verify -> crew done %s\n", t.ID, t.ID)
	return b.String()
}

// RenderMarkdown builds docs/STATUS.md. It is deterministic (no clock) so that
// lint can detect a stale copy.
func RenderMarkdown(m *Model) string {
	s := Summarize(m)
	var b strings.Builder
	b.WriteString("<!-- GENERATED by `go run ./cmd/crew report`. Do not edit by hand. -->\n")
	b.WriteString("# Project status\n\n")
	fmt.Fprintf(&b, "**Overall:** %d/%d tasks done.\n\n", s.DoneTasks, s.TotalTasks)
	if s.CurrentPhase == nil {
		b.WriteString("**Current phase:** none, the roadmap is complete.\n\n")
	} else {
		p := m.Phase(*s.CurrentPhase)
		fmt.Fprintf(&b, "**Current phase:** %d - %s. %s\n\n", p.ID, p.Name, p.Goal)
	}

	b.WriteString("## Phases\n\n| Phase | Name | Progress | Gate |\n| --- | --- | --- | --- |\n")
	for _, p := range s.Phases {
		gate := "open"
		if gp := m.Phase(p.ID); gp != nil && gp.GatePassedAt != "" {
			gate = "passed " + gp.GatePassedAt
		}
		progress := "not planned"
		if p.Planned {
			progress = fmt.Sprintf("%d/%d (%d%%)", p.Done, p.Total, p.Percent())
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", p.ID, p.Name, progress, gate)
	}

	tableOf := func(title string, refs []TaskRef) {
		if len(refs) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		for _, r := range refs {
			fmt.Fprintf(&b, "- `%s` %s\n", r.ID, r.Title)
		}
	}
	tableOf("In progress", s.InProgress)
	tableOf("Blocked", s.Blocked)
	tableOf("Next up", s.Next)

	b.WriteString("\n## Tasks\n")
	for _, p := range m.Phases {
		ts := m.TasksOf(p.ID)
		if len(ts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### Phase %d - %s\n\n| ID | Status | Title | Requirements | Done |\n| --- | --- | --- | --- | --- |\n", p.ID, p.Name)
		for _, t := range ts {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", t.ID, t.Status, t.Title, strings.Join(t.Requirements, ", "), t.CompletedAt)
		}
	}

	b.WriteString("\n## Requirement coverage\n\n| Requirement | Title | Phase | Tasks | State |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range m.Requirements {
		var ids []string
		done := 0
		for _, t := range m.Tasks {
			for _, id := range t.Requirements {
				if id == r.ID {
					ids = append(ids, t.ID)
					if t.Status == Done {
						done++
					}
				}
			}
		}
		state := "unplanned"
		switch {
		case len(ids) > 0 && done == len(ids):
			state = "done"
		case len(ids) > 0 && done > 0:
			state = "in progress"
		case len(ids) > 0:
			state = "planned"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s |\n", r.ID, r.Title, r.Phase, strings.Join(ids, ", "), state)
	}
	return b.String()
}
