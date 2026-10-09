package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/version"
)

const usage = `aotus - command line client of the Aotus daemon

  aotus [--data-dir DIR] <command>

Daemon
  status                          how the daemon is

Subscriptions (profiles)
  profiles                        list profiles
  profile add --kind claude|codex|openai-api --name NAME [--binary PATH] [--mode MODE] [--model MODEL]
  profile detect ID               what the CLI of a profile reports (installed, version, login)
  profile accept ID NOTICE        accept a notice (e.g. claude-headless)
  profile key ID                  store an API key; read from standard input, never from arguments
  profile rm ID

Employees
  employees                       list employees
  employee add --name NAME --profile ID [--role ROLE] [--prompt TEXT]
  employee pause|resume|rm EMPLOYEE
  chat EMPLOYEE PROMPT...         give a prompt and stream the answer   [--approve ask|deny]
  history EMPLOYEE [--limit N]
  terminal start|stop EMPLOYEE    control the employee's terminal (the desktop app shows it)

Approvals
  approvals                       actions waiting for you
  approve ID [--remember none|exact|kind]
  deny ID [--remember none|exact|kind]

EMPLOYEE is a name or an ID. Exit code: 0 ok, 1 failed, 2 bad usage.
`

type app struct {
	ctx    context.Context
	in     io.Reader
	out    io.Writer
	err    io.Writer
	layout datadir.Layout
	c      *client.Client
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func run(ctx context.Context, args []string, in io.Reader, out, errw io.Writer) int {
	global := flag.NewFlagSet("aotus", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dataDir := global.String("data-dir", "", "")
	showVersion := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		fmt.Fprint(errw, usage)
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, "aotus", version.Version)
		return 0
	}
	rest := global.Args()
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Fprint(out, usage)
		return map[bool]int{true: 0, false: 2}[len(rest) > 0]
	}

	a := &app{ctx: ctx, in: in, out: out, err: errw}
	var err error
	if *dataDir != "" {
		a.layout = datadir.Layout{Root: *dataDir}
	} else if a.layout, err = datadir.Default(); err != nil {
		fmt.Fprintln(errw, "aotus:", err)
		return 1
	}
	if a.c, err = client.Discover(ctx, a.layout); err != nil {
		if errors.Is(err, client.ErrNoDaemon) {
			fmt.Fprintln(errw, "aotus: the daemon is not running. Start it with `aotusd` (or open the desktop app).")
			return 1
		}
		fmt.Fprintln(errw, "aotus:", err)
		return 1
	}

	err = a.dispatch(rest)
	var u usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		fmt.Fprintln(errw, "aotus:", err)
		return 2
	default:
		fmt.Fprintln(errw, "aotus:", err)
		return 1
	}
}

func (a *app) dispatch(args []string) error {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "status":
		return a.status()
	case "profiles":
		return a.profiles()
	case "profile":
		return a.profile(rest)
	case "employees":
		return a.employees()
	case "employee":
		return a.employee(rest)
	case "chat":
		return a.chat(rest)
	case "history":
		return a.history(rest)
	case "terminal":
		return a.terminal(rest)
	case "approvals":
		return a.approvals()
	case "approve", "deny":
		return a.answer(cmd == "approve", rest)
	}
	return usageError{fmt.Sprintf("unknown command %q (try `aotus help`)", cmd)}
}

func (a *app) status() error {
	st, err := a.c.Status(a.ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "daemon %s at %s: %s, %d employee(s)\n", st.Version, a.c.Address(), st.Status, st.Employees)
	return nil
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0) }

func (a *app) profiles() error {
	ps, err := a.c.Profiles(a.ctx)
	if err != nil {
		return err
	}
	t := table(a.out)
	fmt.Fprintln(t, "ID\tNAME\tKIND\tMODE\tMODEL")
	for _, p := range ps {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Kind, p.Mode, p.Model)
	}
	return t.Flush()
}

func (a *app) profile(args []string) error {
	if len(args) == 0 {
		return usageError{"profile needs a subcommand: add, detect, accept, key or rm"}
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "add":
		fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var in client.NewProfile
		fs.StringVar(&in.Kind, "kind", "", "")
		fs.StringVar(&in.Name, "name", "", "")
		fs.StringVar(&in.Binary, "binary", "", "")
		fs.StringVar(&in.Mode, "mode", "", "")
		fs.StringVar(&in.Model, "model", "", "")
		fs.StringVar(&in.BaseURL, "base-url", "", "")
		if err := fs.Parse(rest); err != nil || in.Kind == "" || in.Name == "" {
			return usageError{"usage: aotus profile add --kind claude|codex|openai-api --name NAME [--binary PATH] [--mode MODE] [--model MODEL]"}
		}
		p, err := a.c.CreateProfile(a.ctx, in)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, p.ID)
		return nil
	case "detect":
		id, err := one(rest, "profile detect ID")
		if err != nil {
			return err
		}
		d, err := a.c.Detect(a.ctx, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "installed: %v\nversion:   %s (supported: %v)\nmodes:     %s\nlogin:     %s\n", d.Installed, d.Version, d.VersionOK, strings.Join(d.Modes, ", "), d.Login)
		if d.Detail != "" {
			fmt.Fprintln(a.out, "note:     ", d.Detail)
		}
		return nil
	case "accept":
		if len(rest) != 2 {
			return usageError{"usage: aotus profile accept ID NOTICE"}
		}
		return a.c.AcceptNotice(a.ctx, rest[0], rest[1])
	case "key":
		id, err := one(rest, "profile key ID")
		if err != nil {
			return err
		}
		fmt.Fprintln(a.err, "Paste the API key and press Enter (it is not shown again):")
		line, err := bufio.NewReader(a.in).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("no key was read: %w", err)
		}
		return a.c.SetAPIKey(a.ctx, id, strings.TrimSpace(line))
	case "rm":
		id, err := one(rest, "profile rm ID")
		if err != nil {
			return err
		}
		return a.c.DeleteProfile(a.ctx, id)
	}
	return usageError{fmt.Sprintf("unknown profile subcommand %q", args[0])}
}

func one(args []string, usage string) (string, error) {
	if len(args) != 1 {
		return "", usageError{"usage: aotus " + usage}
	}
	return args[0], nil
}

func (a *app) employees() error {
	es, err := a.c.Employees(a.ctx)
	if err != nil {
		return err
	}
	t := table(a.out)
	fmt.Fprintln(t, "ID\tNAME\tROLE\tSTATE\tWORKING\tTERMINAL")
	for _, e := range es {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%v\t%v\n", e.ID, e.Name, e.Role, e.State, e.Working, e.TerminalRunning)
	}
	return t.Flush()
}

// find resolves an employee given by ID or by name.
func (a *app) find(ref string) (client.Employee, error) {
	es, err := a.c.Employees(a.ctx)
	if err != nil {
		return client.Employee{}, err
	}
	for _, e := range es {
		if e.ID == ref {
			return e, nil
		}
	}
	for _, e := range es {
		if strings.EqualFold(e.Name, ref) {
			return e, nil
		}
	}
	return client.Employee{}, fmt.Errorf("no employee called %q (see `aotus employees`)", ref)
}

func (a *app) employee(args []string) error {
	if len(args) == 0 {
		return usageError{"employee needs a subcommand: add, pause, resume or rm"}
	}
	sub, rest := args[0], args[1:]
	if sub == "add" {
		fs := flag.NewFlagSet("employee add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var in client.NewEmployee
		fs.StringVar(&in.Name, "name", "", "")
		fs.StringVar(&in.Role, "role", "", "")
		fs.StringVar(&in.SystemPrompt, "prompt", "", "")
		fs.StringVar(&in.ProfileID, "profile", "", "")
		if err := fs.Parse(rest); err != nil || in.Name == "" || in.ProfileID == "" {
			return usageError{"usage: aotus employee add --name NAME --profile ID [--role ROLE] [--prompt TEXT]"}
		}
		e, err := a.c.CreateEmployee(a.ctx, in)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, e.ID)
		return nil
	}
	ref, err := one(rest, "employee "+sub+" EMPLOYEE")
	if err != nil {
		return err
	}
	e, err := a.find(ref)
	if err != nil {
		return err
	}
	switch sub {
	case "pause":
		return a.c.Pause(a.ctx, e.ID)
	case "resume":
		return a.c.Resume(a.ctx, e.ID)
	case "rm":
		return a.c.DeleteEmployee(a.ctx, e.ID)
	}
	return usageError{fmt.Sprintf("unknown employee subcommand %q", sub)}
}

func (a *app) history(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 10, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return usageError{"usage: aotus history EMPLOYEE [--limit N]"}
	}
	e, err := a.find(fs.Arg(0))
	if err != nil {
		return err
	}
	turns, err := a.c.History(a.ctx, e.ID, *limit, "")
	if err != nil {
		return err
	}
	t := table(a.out)
	fmt.Fprintln(t, "STARTED\tSTATE\tTOKENS\tPROMPT")
	for i := len(turns) - 1; i >= 0; i-- { // oldest first reads naturally
		x := turns[i]
		prompt := strings.ReplaceAll(x.Prompt, "\n", " ")
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		fmt.Fprintf(t, "%s\t%s\t%d/%d\t%s\n", x.StartedAt, x.State, x.InputTokens, x.OutputTokens, prompt)
	}
	return t.Flush()
}

func (a *app) terminal(args []string) error {
	if len(args) != 2 || (args[0] != "start" && args[0] != "stop") {
		return usageError{"usage: aotus terminal start|stop EMPLOYEE"}
	}
	e, err := a.find(args[1])
	if err != nil {
		return err
	}
	if args[0] == "start" {
		return a.c.StartTerminal(a.ctx, e.ID, 0, 0)
	}
	return a.c.StopTerminal(a.ctx, e.ID)
}

func (a *app) approvals() error {
	list, err := a.c.Approvals(a.ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(a.out, "nothing is waiting for you")
		return nil
	}
	t := table(a.out)
	fmt.Fprintln(t, "ID\tEMPLOYEE\tACTION\tTARGET")
	for _, r := range list {
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", r.ID, r.EmployeeID, r.Kind, r.Target)
	}
	return t.Flush()
}

func (a *app) answer(allow bool, args []string) error {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remember := fs.String("remember", "none", "")
	// The ID comes first: `aotus approve req-1 --remember exact`.
	if len(args) == 0 {
		return usageError{"usage: aotus approve|deny ID [--remember none|exact|kind]"}
	}
	id := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return usageError{"usage: aotus approve|deny ID [--remember none|exact|kind]"}
	}
	return a.c.Answer(a.ctx, id, allow, *remember)
}
