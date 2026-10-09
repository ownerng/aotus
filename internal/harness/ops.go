package harness

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Start moves a task to in_progress. It enforces the workflow rules: the task
// belongs to the current phase, its dependencies are done and nothing else is
// in progress.
func (m *Model) Start(id string) error {
	t := m.Task(id)
	if t == nil {
		return fmt.Errorf("unknown task %s", id)
	}
	if t.Status != Todo && t.Status != Blocked {
		return fmt.Errorf("task %s is %s; only todo or blocked tasks can be started", id, t.Status)
	}
	cur := m.CurrentPhase()
	if cur == nil || t.Phase != cur.ID {
		return fmt.Errorf("task %s is in phase %d, which is not the current phase", id, t.Phase)
	}
	for _, d := range t.DependsOn {
		if dep := m.Task(d); dep == nil || dep.Status != Done {
			return fmt.Errorf("task %s depends on %s, which is not done", id, d)
		}
	}
	for _, o := range m.Tasks {
		if o.Status == InProgress && o.ID != id {
			return fmt.Errorf("task %s is already in progress; finish it with `crew done` or park it with `crew block`", o.ID)
		}
	}
	t.Status = InProgress
	return nil
}

// Block parks an in-progress task and records why.
func (m *Model) Block(id, reason string) error {
	t := m.Task(id)
	if t == nil {
		return fmt.Errorf("unknown task %s", id)
	}
	if t.Status != InProgress {
		return fmt.Errorf("task %s is %s; only in_progress tasks can be blocked", id, t.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("a reason is required")
	}
	t.Status = Blocked
	t.Notes = strings.TrimSpace(reason)
	return nil
}

// Complete marks an in-progress task as done. Callers must have run the checks
// first; this only performs the state change.
func (m *Model) Complete(id string, now time.Time) error {
	t := m.Task(id)
	if t == nil {
		return fmt.Errorf("unknown task %s", id)
	}
	if t.Status != InProgress {
		return fmt.Errorf("task %s is %s; start it with `crew start %s` first", id, t.Status, id)
	}
	t.Status = Done
	t.CompletedAt = now.UTC().Format("2006-01-02")
	return nil
}

// PassGate records that the exit gate of a phase has been passed. It requires
// every task of the phase to be done and every earlier gate to be passed.
func (m *Model) PassGate(phase int, now time.Time) error {
	p := m.Phase(phase)
	if p == nil {
		return fmt.Errorf("unknown phase %d", phase)
	}
	cur := m.CurrentPhase()
	if cur == nil || cur.ID != phase {
		return fmt.Errorf("phase %d is not the current phase", phase)
	}
	if !p.Planned {
		return fmt.Errorf("phase %d has no tasks yet", phase)
	}
	for _, t := range m.TasksOf(phase) {
		if t.Status != Done {
			return fmt.Errorf("task %s is %s; every task must be done before the gate", t.ID, t.Status)
		}
	}
	p.GatePassedAt = now.UTC().Format("2006-01-02")
	return nil
}
