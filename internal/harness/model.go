// Package harness is the project-management brain of the repository: it loads
// phases, requirements and tasks from harness/*.json, validates them, tracks
// progress and runs the checks that prove a task or a phase is done.
//
// It deliberately depends on the standard library only, so that the harness
// keeps working even when the product code does not compile.
package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Paths, relative to the repository root.
const (
	PhasesFile       = "harness/phases.json"
	RequirementsFile = "harness/requirements.json"
	ArchitectureFile = "harness/architecture.json"
	TasksDir         = "harness/tasks"
	StatusFile       = "docs/STATUS.md"
)

// Status is the lifecycle state of a task.
type Status string

// Task lifecycle states.
const (
	Todo       Status = "todo"
	InProgress Status = "in_progress"
	Blocked    Status = "blocked"
	Done       Status = "done"
)

func (s Status) valid() bool {
	switch s {
	case Todo, InProgress, Blocked, Done:
		return true
	}
	return false
}

// Check is one machine-verifiable piece of evidence. Exactly one kind must be
// set:
//
//   - Cmd: the command must exit 0 (run from the repository root).
//   - File: the file must exist and be non-empty; if Contains is set it must
//     also contain every listed substring.
//   - Pkg + Tests: every named Go test must exist in the package and pass. A
//     plain `go test` also passes when there are no tests, which would let a
//     task be "done" without proof; naming the tests closes that hole.
type Check struct {
	Name     string   `json:"name"`
	Cmd      []string `json:"cmd,omitempty"`
	File     string   `json:"file,omitempty"`
	Contains []string `json:"contains,omitempty"`
	Pkg      string   `json:"pkg,omitempty"`
	Tests    []string `json:"tests,omitempty"`
	// Tags are Go build tags used when running Pkg's tests (for example
	// "desktop" for the Wails app, which needs native libraries).
	Tags []string `json:"tags,omitempty"`
}

// Task is the unit of work. A task is done only when all its Checks pass.
type Task struct {
	ID           string   `json:"id"`
	Phase        int      `json:"phase"`
	Title        string   `json:"title"`
	Status       Status   `json:"status"`
	DependsOn    []string `json:"depends_on,omitempty"`
	Requirements []string `json:"requirements,omitempty"`
	Description  string   `json:"description"`
	Acceptance   []string `json:"acceptance"`
	Checks       []Check  `json:"checks"`
	Paths        []string `json:"paths,omitempty"`
	Docs         []string `json:"docs,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	CompletedAt  string   `json:"completed_at,omitempty"`
}

// Phase is a roadmap stage with an exit gate.
type Phase struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Goal         string   `json:"goal"`
	Planned      bool     `json:"planned"`
	Gate         []string `json:"gate"`
	GateChecks   []Check  `json:"gate_checks,omitempty"`
	GatePassedAt string   `json:"gate_passed_at,omitempty"`
}

// Requirement is a PRD requirement that tasks trace back to.
type Requirement struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Phase int    `json:"phase"`
	Kind  string `json:"kind"` // "functional" or "nonfunctional"
}

// Model is the whole harness state loaded from disk.
type Model struct {
	Root         string
	Phases       []Phase
	Requirements []Requirement
	Tasks        []*Task

	taskFile map[string]string // task ID -> file it was loaded from
}

// Load reads the harness files under root. Unknown JSON fields are rejected so
// that typos in task files fail loudly instead of being silently ignored.
func Load(root string) (*Model, error) {
	m := &Model{Root: root, taskFile: map[string]string{}}
	if err := readJSON(filepath.Join(root, filepath.FromSlash(PhasesFile)), &m.Phases); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(root, filepath.FromSlash(RequirementsFile)), &m.Requirements); err != nil {
		return nil, err
	}
	sort.SliceStable(m.Phases, func(i, j int) bool { return m.Phases[i].ID < m.Phases[j].ID })

	files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(TasksDir), "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, f := range files {
		var ts []*Task
		if err := readJSON(f, &ts); err != nil {
			return nil, err
		}
		for _, t := range ts {
			m.Tasks = append(m.Tasks, t)
			m.taskFile[t.ID] = f
		}
	}
	return m, nil
}

// Save writes phases and tasks back to disk, preserving the file each task
// came from and the order of tasks inside it.
func (m *Model) Save() error {
	if err := writeJSON(filepath.Join(m.Root, filepath.FromSlash(PhasesFile)), m.Phases); err != nil {
		return err
	}
	byFile := map[string][]*Task{}
	for _, t := range m.Tasks {
		f := m.taskFile[t.ID]
		if f == "" {
			f = filepath.Join(m.Root, filepath.FromSlash(TasksDir), fmt.Sprintf("phase-%d.json", t.Phase))
		}
		byFile[f] = append(byFile[f], t)
	}
	for f, ts := range byFile {
		if err := writeJSON(f, ts); err != nil {
			return err
		}
	}
	return nil
}

// Task returns the task with the given ID, or nil.
func (m *Model) Task(id string) *Task {
	for _, t := range m.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Phase returns the phase with the given ID, or nil.
func (m *Model) Phase(id int) *Phase {
	for i := range m.Phases {
		if m.Phases[i].ID == id {
			return &m.Phases[i]
		}
	}
	return nil
}

// TasksOf returns the tasks of a phase in file order.
func (m *Model) TasksOf(phase int) []*Task {
	var out []*Task
	for _, t := range m.Tasks {
		if t.Phase == phase {
			out = append(out, t)
		}
	}
	return out
}

// Requirement returns the requirement with the given ID, or nil.
func (m *Model) Requirement(id string) *Requirement {
	for i := range m.Requirements {
		if m.Requirements[i].ID == id {
			return &m.Requirements[i]
		}
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) //nolint:gosec // path is built from the repo root
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644) //nolint:gosec // repo files are world-readable
}
