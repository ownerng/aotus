// Command crew is the project harness CLI. It tells any developer or model
// where the project stands, what to build next, and whether the work is done.
//
// Run `go run ./cmd/crew help` for the commands.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aotus/internal/harness"
)

const usage = `crew - project harness

Orientation
  status [--json]        phase, progress, what is in progress, what is next
  next [--json]          brief of the task to work on now (in progress first)
  show <task>            brief of any task

Workflow
  start <task>           claim a task (one at a time, dependencies must be done)
  verify [--full] [--strict] [<task>]
                         no task: run the quality gates
                         with task: run that task's own checks
  done <task>            run task checks + quality gates, then mark it done
  block <task> <reason>  park the task you are working on
  gate <phase>           run the phase exit gate and close the phase

Maintenance
  lint                   validate harness files (also part of verify)
  arch                   check the architecture rules (also part of verify)
  report                 regenerate docs/STATUS.md

Run from anywhere inside the repository. Exit code is 0 on success, 1 on a
failed check, 2 on bad usage.
`

type app struct {
	root string
	out  io.Writer
	err  io.Writer
	now  func() time.Time
}

func main() {
	root, err := findRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "crew:", err)
		os.Exit(2)
	}
	a := &app{root: root, out: os.Stdout, err: os.Stderr, now: time.Now}
	os.Exit(a.run(os.Args[1:]))
}

// findRoot walks up from the working directory to the directory that holds
// harness/phases.json.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(harness.PhasesFile))); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside the repository: harness/phases.json not found")
		}
		dir = parent
	}
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.err, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(a.out, usage)
		return 0
	case "status":
		err = a.status(rest)
	case "next":
		err = a.next(rest)
	case "show":
		err = a.show(rest)
	case "start":
		err = a.start(rest)
	case "verify":
		err = a.verify(rest)
	case "done":
		err = a.done(rest)
	case "block":
		err = a.block(rest)
	case "gate":
		err = a.gate(rest)
	case "lint":
		err = a.lint(rest)
	case "arch":
		err = a.arch(rest)
	case "report":
		err = a.report(rest)
	default:
		fmt.Fprintf(a.err, "crew: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	var u usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		fmt.Fprintln(a.err, "crew:", err)
		return 2
	default:
		fmt.Fprintln(a.err, "crew:", err)
		return 1
	}
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func (a *app) flags(name string, args []string, setup func(*flag.FlagSet)) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	setup(fs)
	if err := fs.Parse(args); err != nil {
		return nil, usageError{fmt.Sprintf("%s: %v", name, err)}
	}
	return fs, nil
}

func (a *app) load() (*harness.Model, error) { return harness.Load(a.root) }

// save persists the model and refreshes docs/STATUS.md so the two never drift.
func (a *app) save(m *harness.Model) error {
	if err := m.Save(); err != nil {
		return err
	}
	return a.writeReport(m)
}

func (a *app) writeReport(m *harness.Model) error {
	path := filepath.Join(a.root, filepath.FromSlash(harness.StatusFile))
	return os.WriteFile(path, []byte(harness.RenderMarkdown(m)), 0o644) //nolint:gosec // docs are world-readable
}

func (a *app) status(args []string) error {
	var asJSON bool
	if _, err := a.flags("status", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "") }); err != nil {
		return err
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	if asJSON {
		return a.printJSON(harness.Summarize(m))
	}
	fmt.Fprint(a.out, harness.RenderText(m))
	return nil
}

func (a *app) next(args []string) error {
	var asJSON bool
	if _, err := a.flags("next", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "") }); err != nil {
		return err
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	var t *harness.Task
	for _, c := range m.Tasks {
		if c.Status == harness.InProgress {
			t = c
			break
		}
	}
	if t == nil {
		if el := m.Eligible(); len(el) > 0 {
			t = el[0]
		}
	}
	if t == nil {
		cur := m.CurrentPhase()
		switch {
		case cur == nil:
			fmt.Fprintln(a.out, "The roadmap is complete.")
		case !cur.Planned:
			fmt.Fprintf(a.out, "Phase %d (%s) has no tasks: plan it before building (add harness/tasks/phase-%d.json).\n", cur.ID, cur.Name, cur.ID)
		case harness.Summarize(m).Phases[phaseIndex(m, cur.ID)].ReadyForGate:
			fmt.Fprintf(a.out, "All tasks of phase %d are done: run `crew gate %d`.\n", cur.ID, cur.ID)
		default:
			fmt.Fprintln(a.out, "No task can start: the remaining ones are blocked or wait on dependencies. Run `crew status`.")
		}
		return nil
	}
	if asJSON {
		return a.printJSON(t)
	}
	fmt.Fprint(a.out, harness.RenderBrief(m, t))
	return nil
}

func phaseIndex(m *harness.Model, id int) int {
	for i, p := range m.Phases {
		if p.ID == id {
			return i
		}
	}
	return 0
}

func (a *app) show(args []string) error {
	if len(args) != 1 {
		return usageError{"usage: crew show <task>"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	t := m.Task(args[0])
	if t == nil {
		return fmt.Errorf("unknown task %s", args[0])
	}
	fmt.Fprint(a.out, harness.RenderBrief(m, t))
	return nil
}

func (a *app) start(args []string) error {
	if len(args) != 1 {
		return usageError{"usage: crew start <task>"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	if err := m.Start(args[0]); err != nil {
		return err
	}
	if err := a.save(m); err != nil {
		return err
	}
	fmt.Fprint(a.out, harness.RenderBrief(m, m.Task(args[0])))
	return nil
}

func (a *app) block(args []string) error {
	if len(args) < 2 {
		return usageError{"usage: crew block <task> <reason>"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	if err := m.Block(args[0], strings.Join(args[1:], " ")); err != nil {
		return err
	}
	if err := a.save(m); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "%s is now blocked.\n", args[0])
	return nil
}

func (a *app) verify(args []string) error {
	var opt harness.GateOptions
	fs, err := a.flags("verify", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&opt.Full, "full", false, "")
		fs.BoolVar(&opt.Strict, "strict", false, "")
	})
	if err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageError{"usage: crew verify [--full] [--strict] [<task>]"}
	}
	if fs.NArg() == 1 {
		m, err := a.load()
		if err != nil {
			return err
		}
		t := m.Task(fs.Arg(0))
		if t == nil {
			return fmt.Errorf("unknown task %s", fs.Arg(0))
		}
		return a.report1("task "+t.ID, harness.RunChecks(a.root, t.Checks))
	}
	return a.report1("quality gates", harness.ProjectChecks(a.root, opt))
}

// report1 prints results and returns an error when any check failed.
func (a *app) report1(title string, rs []harness.Result) error {
	fmt.Fprintf(a.out, "== %s\n", title)
	for _, r := range rs {
		tag := map[harness.Outcome]string{harness.Pass: "PASS", harness.Fail: "FAIL", harness.Skip: "SKIP"}[r.Outcome]
		fmt.Fprintf(a.out, "[%s] %s (%s)\n", tag, r.Name, r.Elapsed.Round(time.Millisecond))
		if r.Outcome != harness.Pass && r.Output != "" {
			for _, line := range strings.Split(r.Output, "\n") {
				fmt.Fprintf(a.out, "       %s\n", line)
			}
		}
	}
	if f, ok := harness.FirstFailure(rs); ok {
		return fmt.Errorf("%s failed: %s", title, f.Name)
	}
	return nil
}

func (a *app) done(args []string) error {
	if len(args) != 1 {
		return usageError{"usage: crew done <task>"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	t := m.Task(args[0])
	if t == nil {
		return fmt.Errorf("unknown task %s", args[0])
	}
	if t.Status != harness.InProgress {
		return fmt.Errorf("task %s is %s; run `crew start %s` first", t.ID, t.Status, t.ID)
	}
	if err := a.report1("task "+t.ID, harness.RunChecks(a.root, t.Checks)); err != nil {
		return err
	}
	// Quality gates must pass with the task marked done, so mark first and
	// roll back on failure.
	if err := m.Complete(t.ID, a.now()); err != nil {
		return err
	}
	if err := a.save(m); err != nil {
		return err
	}
	if err := a.report1("quality gates", harness.ProjectChecks(a.root, harness.GateOptions{})); err != nil {
		t.Status, t.CompletedAt = harness.InProgress, ""
		if saveErr := a.save(m); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return fmt.Errorf("%w; %s stays in_progress", err, t.ID)
	}
	fmt.Fprintf(a.out, "\n%s is done.\n", t.ID)
	return a.printNextHint(m)
}

func (a *app) printNextHint(m *harness.Model) error {
	cur := m.CurrentPhase()
	if cur == nil {
		fmt.Fprintln(a.out, "The roadmap is complete.")
		return nil
	}
	if harness.Summarize(m).Phases[phaseIndex(m, cur.ID)].ReadyForGate {
		fmt.Fprintf(a.out, "All tasks of phase %d are done: run `crew gate %d`.\n", cur.ID, cur.ID)
		return nil
	}
	fmt.Fprintln(a.out, "Run `crew next` for the next task.")
	return nil
}

func (a *app) gate(args []string) error {
	if len(args) != 1 {
		return usageError{"usage: crew gate <phase>"}
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return usageError{"usage: crew gate <phase number>"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	p := m.Phase(n)
	if p == nil {
		return fmt.Errorf("unknown phase %d", n)
	}
	fmt.Fprintf(a.out, "Gate criteria for phase %d (%s):\n", p.ID, p.Name)
	for _, c := range p.Gate {
		fmt.Fprintf(a.out, "  - %s\n", c)
	}
	fmt.Fprintln(a.out)
	if err := a.report1("phase gate checks", harness.RunChecks(a.root, p.GateChecks)); err != nil {
		return err
	}
	if err := a.report1("quality gates (full)", harness.ProjectChecks(a.root, harness.GateOptions{Full: true, Strict: false})); err != nil {
		return err
	}
	if err := m.PassGate(n, a.now()); err != nil {
		return err
	}
	if err := a.save(m); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "\nPhase %d gate passed.\n", n)
	return nil
}

func (a *app) lint(args []string) error {
	if len(args) != 0 {
		return usageError{"usage: crew lint"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	issues := harness.Lint(m)
	for _, i := range issues {
		fmt.Fprintln(a.out, i)
	}
	if harness.HasErrors(issues) {
		return errors.New("harness lint failed")
	}
	fmt.Fprintln(a.out, "harness lint: ok")
	return nil
}

func (a *app) arch(args []string) error {
	if len(args) != 0 {
		return usageError{"usage: crew arch"}
	}
	v, err := harness.Arch(a.root)
	if err != nil {
		return err
	}
	for _, line := range v {
		fmt.Fprintln(a.out, line)
	}
	if len(v) > 0 {
		return fmt.Errorf("%d architecture violation(s)", len(v))
	}
	fmt.Fprintln(a.out, "architecture: ok")
	return nil
}

func (a *app) report(args []string) error {
	if len(args) != 0 {
		return usageError{"usage: crew report"}
	}
	m, err := a.load()
	if err != nil {
		return err
	}
	if err := a.writeReport(m); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "wrote %s\n", harness.StatusFile)
	return nil
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
