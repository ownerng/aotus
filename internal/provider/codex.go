package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aotus/internal/proc"
)

// Codex is the adapter for the official Codex CLI (codex exec --json).
type Codex struct {
	// Lookup reads the daemon's environment for the allow-list; nil means the
	// real one.
	Lookup func(string) (string, bool)
}

// Kind implements Provider.
func (Codex) Kind() Kind { return KindCodex }

func (c Codex) lookup() func(string) (string, bool) {
	if c.Lookup != nil {
		return c.Lookup
	}
	return OSEnv
}

// Detect implements Provider.
func (c Codex) Detect(ctx context.Context, p Profile) (Detection, error) {
	modes := []Mode{ModeStructured}
	if proc.PTYSupported() {
		modes = append(modes, ModeTerminal)
	}
	return detectCLI(ctx, p, "Codex CLI", modes, c.lookup())
}

// Preflight returns one of the typed errors when the CLI cannot be used.
func (c Codex) Preflight(ctx context.Context, p Profile) error {
	d, err := c.Detect(ctx, p)
	if err != nil {
		return err
	}
	return d.Err()
}

// Start implements Provider. Codex CLI's default mode is structured.
func (c Codex) Start(_ context.Context, req SessionRequest) (Session, error) {
	switch mode := firstMode(req.Mode, req.Profile.Mode, ModeStructured); mode {
	case ModeStructured:
		return NewProcessSession(ModeStructured, codexDialect{lookup: c.lookup()}, req), nil
	case ModeTerminal:
		if !proc.PTYSupported() {
			return nil, fmt.Errorf("%w: the terminal mode is not available on this platform yet", ErrUnsupportedMode)
		}
		// Codex does not let us choose a session ID: it resumes the most recent
		// session of the working folder, which is the employee's own.
		return newTerminalSession(req, codexLauncher{}, req.Profile.Binary, MergeEnv(ProfileEnv(req.Profile, c.lookup()), req.ExtraEnv), false), nil
	default:
		return nil, fmt.Errorf("%w: Codex CLI supports %v, not %q", ErrUnsupportedMode, []Mode{ModeStructured, ModeTerminal}, mode)
	}
}

// LoginSession returns a terminal session that runs Codex CLI's own login
// (codex login) with the profile's environment. Aotus never sees the
// credentials.
func (c Codex) LoginSession(p Profile) (TerminalSession, error) {
	if !proc.PTYSupported() {
		return nil, fmt.Errorf("%w: the terminal is not available on this platform yet", ErrUnsupportedMode)
	}
	return newTerminalSession(SessionRequest{Profile: p}, fixedLauncher{args: []string{"login"}}, p.Binary, ProfileEnv(p, c.lookup()), false), nil
}

// codexLauncher builds the command lines of the interactive Codex CLI.
type codexLauncher struct{}

func codexFlags(req SessionRequest) []string {
	args := []string{"-s", firstNonEmpty(req.PermissionMode, codexSandbox)}
	if m := firstNonEmpty(req.Model, req.Profile.Model); m != "" {
		args = append(args, "-m", m)
	}
	return args
}

func (codexLauncher) New(req SessionRequest, _ string) []string { return codexFlags(req) }

func (codexLauncher) Resume(req SessionRequest, id string) []string {
	args := []string{"resume"}
	if id == "" {
		args = append(args, "--last")
	} else {
		args = append(args, id)
	}
	if m := firstNonEmpty(req.Model, req.Profile.Model); m != "" {
		args = append(args, "-m", m)
	}
	return args
}

// fixedLauncher always runs the same arguments (the login commands).
type fixedLauncher struct{ args []string }

func (f fixedLauncher) New(SessionRequest, string) []string    { return f.args }
func (f fixedLauncher) Resume(SessionRequest, string) []string { return f.args }

func firstMode(modes ...Mode) Mode {
	for _, m := range modes {
		if m != "" {
			return m
		}
	}
	return ""
}

// codexDialect builds `codex exec --json` turns.
type codexDialect struct {
	lookup func(string) (string, bool)
}

// codexSandbox is used when the employee's permissions do not name one.
const codexSandbox = "read-only"

// Turn implements Dialect. The prompt goes on stdin ("-"), which keeps it out
// of the process list.
func (d codexDialect) Turn(t TurnRequest) (Command, Parser, error) {
	p := t.Request.Profile
	sandbox := firstNonEmpty(t.Request.PermissionMode, codexSandbox)
	model := firstNonEmpty(t.Request.Model, p.Model)

	var args []string
	if t.SessionID == "" {
		args = []string{"exec", "--json", "--skip-git-repo-check", "-s", sandbox}
		if model != "" {
			args = append(args, "-m", model)
		}
		args = append(args, "-")
	} else {
		// `exec resume` has no -s: the sandbox is passed as configuration.
		// (Accepted flags checked in docs/research/cli-contracts.md; the
		// config key is unverified until a successful turn is recorded.)
		args = []string{"exec", "resume", "--json", "--skip-git-repo-check", "-c", fmt.Sprintf("sandbox_mode=%q", sandbox)}
		if model != "" {
			args = append(args, "-m", model)
		}
		args = append(args, t.SessionID, "-")
	}
	return Command{
		Spec:  proc.Spec{Path: p.Binary, Args: args, Dir: t.Request.Dir, Env: MergeEnv(ProfileEnv(p, d.lookup), t.Request.ExtraEnv)},
		Stdin: t.Prompt,
	}, &codexParser{requested: map[string]bool{}}, nil
}

// codexParser reads the JSON lines of one `codex exec --json` turn.
//
// Observed on 2026-10-09 (failure path only): thread.started, turn.started,
// item.completed with an item of type "error" (a warning), error, turn.failed.
// The success path (items of type agent_message, command_execution, ... and
// turn.completed) follows Codex's public documentation and has not been
// recorded yet: see docs/research/cli-contracts.md, section 5.
type codexParser struct {
	requested map[string]bool
	lastError string
	gotDone   bool
}

type codexLine struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Item     *struct {
		ID               string          `json:"id"`
		Type             string          `json:"type"`
		Text             string          `json:"text"`
		Command          string          `json:"command"`
		AggregatedOutput string          `json:"aggregated_output"`
		ExitCode         *int            `json:"exit_code"`
		Status           string          `json:"status"`
		Server           string          `json:"server"`
		Tool             string          `json:"tool"`
		Query            string          `json:"query"`
		Changes          json.RawMessage `json:"changes"`
		Arguments        json.RawMessage `json:"arguments"`
		Message          string          `json:"message"`
	} `json:"item"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Parse implements Parser. Unknown types and fields are ignored.
func (c *codexParser) Parse(l proc.Line) []Event {
	if l.Stream != proc.Stdout || !strings.HasPrefix(strings.TrimSpace(l.Text), "{") {
		return nil
	}
	var m codexLine
	if json.Unmarshal([]byte(l.Text), &m) != nil {
		return nil
	}
	switch m.Type {
	case "thread.started":
		if m.ThreadID != "" {
			return []Event{{Kind: EventSession, SessionID: m.ThreadID}}
		}
	case "error":
		c.lastError = m.Message
	case "turn.completed":
		c.gotDone = true
		done := &Done{Reason: DoneCompleted}
		if m.Usage != nil {
			done.InputTokens, done.OutputTokens = m.Usage.InputTokens, m.Usage.OutputTokens
		}
		return []Event{{Kind: EventDone, Done: done}}
	case "turn.failed":
		c.gotDone = true
		msg := c.lastError
		if m.Error != nil && m.Error.Message != "" {
			msg = m.Error.Message
		}
		text, code := classifyCodexError(msg)
		return []Event{
			{Kind: EventError, Code: code, Text: text},
			{Kind: EventDone, Done: &Done{Reason: DoneFailed}},
		}
	case "item.started", "item.updated", "item.completed":
		if m.Item != nil {
			return c.item(m.Type, m)
		}
	}
	return nil
}

func (c *codexParser) request(id, name string, input json.RawMessage) Event {
	c.requested[id] = true
	return Event{Kind: EventToolRequest, Tool: &Tool{ID: id, Name: name, Input: input}}
}

func (c *codexParser) item(kind string, m codexLine) []Event {
	it := m.Item
	completed := kind == "item.completed"
	switch it.Type {
	case "agent_message":
		if completed && it.Text != "" {
			return []Event{{Kind: EventText, Text: it.Text}}
		}
	case "command_execution":
		var out []Event
		if !c.requested[it.ID] {
			in, _ := json.Marshal(map[string]string{"command": it.Command})
			out = append(out, c.request(it.ID, "command", in))
		}
		if completed {
			failed := (it.ExitCode != nil && *it.ExitCode != 0) || it.Status == "failed"
			out = append(out, Event{Kind: EventToolResult, Tool: &Tool{ID: it.ID, Output: it.AggregatedOutput, IsError: failed}})
		}
		return out
	case "file_change", "mcp_tool_call", "web_search":
		name, input := it.Type, json.RawMessage(nil)
		switch it.Type {
		case "file_change":
			input = it.Changes
		case "mcp_tool_call":
			name, input = strings.Trim(it.Server+"."+it.Tool, "."), it.Arguments
		case "web_search":
			input, _ = json.Marshal(map[string]string{"query": it.Query})
		}
		var out []Event
		if !c.requested[it.ID] {
			out = append(out, c.request(it.ID, name, input))
		}
		if completed {
			out = append(out, Event{Kind: EventToolResult, Tool: &Tool{ID: it.ID, IsError: it.Status == "failed"}})
		}
		return out
	}
	// "reasoning", "todo_list" and warnings (item type "error") are not shown.
	return nil
}

// classifyCodexError extracts the human message from the JSON string Codex
// puts in its error events and maps it to a code.
func classifyCodexError(raw string) (string, ErrorCode) {
	msg := strings.TrimSpace(raw)
	var wrapped struct {
		Status int `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	status := 0
	if json.Unmarshal([]byte(msg), &wrapped) == nil && wrapped.Error.Message != "" {
		msg, status = wrapped.Error.Message, wrapped.Status
	}
	if msg == "" {
		msg = "Codex CLI reported an error"
	}
	l := strings.ToLower(msg)
	switch {
	case status == 401, strings.Contains(l, "token has expired"), strings.Contains(l, "not logged in"), strings.Contains(l, "log in"), strings.Contains(l, "authentication"):
		return msg, CodeNeedsLogin
	case status == 429, strings.Contains(l, "rate limit"), strings.Contains(l, "usage limit"):
		return msg, CodeRateLimited
	case strings.Contains(l, "model") && (strings.Contains(l, "not supported") || strings.Contains(l, "not found")):
		return msg, CodeModelUnsupported
	}
	return msg, CodeCLI
}

// OnExit implements Parser. Codex logs authentication failures to stderr (for
// example "401 Unauthorized ... Your authentication token has expired") even
// while its login status command still says "Logged in".
func (c *codexParser) OnExit(exit proc.Exit, recent []proc.Line) []Event {
	if c.gotDone || exit.Code == 0 || exit.Canceled {
		return nil
	}
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Stream != proc.Stderr {
			continue
		}
		if msg, code := classifyCodexError(recent[i].Text); code == CodeNeedsLogin {
			return []Event{{Kind: EventError, Code: code, Text: msg}}
		}
	}
	return nil
}
