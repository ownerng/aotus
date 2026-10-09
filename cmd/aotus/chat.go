package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"aotus/internal/client"
)

// chat gives an employee a prompt and streams the answer to standard output,
// showing tool use and problems on standard error. Actions that need approval
// are asked about on standard error, unless --approve=deny.
func (a *app) chat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	approve := fs.String("approve", "ask", "")
	// The employee comes first: `aotus chat Atlas what is new --approve deny`.
	if len(args) < 2 {
		return usageError{"usage: aotus chat EMPLOYEE PROMPT... [--approve ask|deny]"}
	}
	ref := args[0]
	var words []string
	rest := args[1:]
	for i, w := range rest {
		if strings.HasPrefix(w, "--") {
			if err := fs.Parse(rest[i:]); err != nil {
				return usageError{"usage: aotus chat EMPLOYEE PROMPT... [--approve ask|deny]"}
			}
			break
		}
		words = append(words, w)
	}
	if *approve != "ask" && *approve != "deny" {
		return usageError{`--approve takes "ask" or "deny"`}
	}
	prompt := strings.Join(words, " ")
	if strings.TrimSpace(prompt) == "" {
		return usageError{"the prompt is empty"}
	}
	e, err := a.find(ref)
	if err != nil {
		return err
	}

	stream, err := a.c.Events(a.ctx, e.ID)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(a.ctx); err != nil { // hello
		return err
	}
	turnID, err := a.c.Send(a.ctx, e.ID, prompt)
	if err != nil {
		return err
	}
	if turnID == "" {
		fmt.Fprintln(a.err, "the prompt was typed into the employee's terminal; open the desktop app to see it")
		return nil
	}

	stdin := bufio.NewReader(a.in)
	wroteText := false
	for {
		m, err := stream.Next(a.ctx)
		if err != nil {
			if a.ctx.Err() != nil { // Ctrl+C: stop the work as well
				stop, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
				_ = a.c.Cancel(stop, e.ID)
				cancel()
				return errors.New("interrupted: the turn was canceled")
			}
			if client.TooSlow(err) {
				return errors.New("this terminal could not keep up with the events; see `aotus history`")
			}
			return err
		}
		switch {
		case m.Approval != nil && m.Approval.EmployeeID == e.ID:
			a.askApproval(stdin, *m.Approval, *approve == "deny")
		case m.Update != nil && m.Update.TurnID == turnID:
			if done, err := a.show(m.Update, &wroteText); done {
				return err
			}
		}
	}
}

// show prints one update and reports whether the turn is over.
func (a *app) show(u *client.Update, wroteText *bool) (bool, error) {
	switch u.Kind {
	case "event":
		ev := u.Event
		switch ev.Kind {
		case "text":
			fmt.Fprint(a.out, ev.Text)
			*wroteText = true
		case "tool_request":
			fmt.Fprintf(a.err, "\n[%s]\n", ev.ToolName)
		case "error":
			fmt.Fprintf(a.err, "\nerror: %s\n", ev.Text)
		}
	case "turn_ended":
		if *wroteText {
			fmt.Fprintln(a.out)
		}
		switch u.State {
		case "completed":
			return true, nil
		case "canceled":
			return true, errors.New("the turn was canceled")
		default:
			if u.Detail != "" {
				return true, fmt.Errorf("the turn %s: %s", u.State, u.Detail)
			}
			return true, fmt.Errorf("the turn %s", u.State)
		}
	}
	return false, nil
}

// askApproval answers an approval request, asking the user unless denying.
func (a *app) askApproval(in *bufio.Reader, r client.Approval, denyAll bool) {
	answer, remember := false, "none"
	if !denyAll {
		fmt.Fprintf(a.err, "\n%s wants to %s: %s\nAllow? [y]es once / [n]o / [e]xactly this always / [k]ind always: ", r.EmployeeID, r.Kind, r.Target)
		line, _ := in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			answer = true
		case "e":
			answer, remember = true, "exact"
		case "k":
			answer, remember = true, "kind"
		}
	}
	if err := a.c.Answer(a.ctx, r.ID, answer, remember); err != nil {
		fmt.Fprintln(a.err, "could not answer:", err)
	}
}
