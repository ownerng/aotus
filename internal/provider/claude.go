package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"aotus/internal/proc"
)

// NoticeClaudeHeadless guards Claude Code's structured mode. Its text must be
// shown to the user, who accepts it per profile.
const NoticeClaudeHeadless = "claude-headless"

// ClaudeHeadlessNotice is the text of NoticeClaudeHeadless.
const ClaudeHeadlessNotice = "Structured mode runs the official Claude Code in its non-interactive mode (claude -p) with your own subscription. " +
	"Anthropic's terms are ambiguous about automated use of a subscription this way, and its billing for this mode has changed several times. " +
	"The terminal mode, which shows the official interface, avoids that question. Use structured mode only if you accept the risk."

// Claude is the adapter for the official Claude Code CLI.
type Claude struct {
	// Lookup reads the daemon's environment for the allow-list; nil means the
	// real one.
	Lookup func(string) (string, bool)
}

// Kind implements Provider.
func (Claude) Kind() Kind { return KindClaude }

func (c Claude) lookup() func(string) (string, bool) {
	if c.Lookup != nil {
		return c.Lookup
	}
	return OSEnv
}

// Detect implements Provider.
func (c Claude) Detect(ctx context.Context, p Profile) (Detection, error) {
	modes := []Mode{ModeStructured}
	if proc.PTYSupported() {
		modes = []Mode{ModeTerminal, ModeStructured} // terminal first: it is the default
	}
	return detectCLI(ctx, p, "Claude Code", modes, c.lookup())
}

// Preflight returns one of the typed errors (ErrCLINotInstalled,
// ErrUnsupportedVersion, ErrNeedsLogin) when the CLI cannot be used, or nil.
func (c Claude) Preflight(ctx context.Context, p Profile) error {
	d, err := c.Detect(ctx, p)
	if err != nil {
		return err
	}
	return d.Err()
}

// Start implements Provider. Claude Code's default mode is terminal: the
// official interactive UI, hosted unmodified (docs/research/provider-terms.md,
// Decision). Structured mode runs `claude -p` and needs the user to have
// accepted a notice.
func (c Claude) Start(_ context.Context, req SessionRequest) (Session, error) {
	switch mode := firstMode(req.Mode, req.Profile.Mode, ModeTerminal); mode {
	case ModeTerminal:
		if !proc.PTYSupported() {
			return nil, fmt.Errorf("%w: the terminal mode is not available on this platform yet", ErrUnsupportedMode)
		}
		env := ProfileEnv(req.Profile, c.lookup())
		return newTerminalSession(req, claudeLauncher{}, req.Profile.Binary, env, true), nil
	case ModeStructured:
		if !slices.Contains(req.Profile.AcceptedNotices, NoticeClaudeHeadless) {
			return nil, fmt.Errorf("%w: %s", ErrNoticeRequired, ClaudeHeadlessNotice)
		}
		return NewProcessSession(ModeStructured, claudeDialect{lookup: c.lookup()}, req), nil
	default:
		return nil, fmt.Errorf("%w: Claude Code supports %v, not %q", ErrUnsupportedMode, []Mode{ModeTerminal, ModeStructured}, mode)
	}
}

// LoginSession returns a terminal session that runs Claude Code's own login
// (claude auth login) with the profile's environment, so the user signs in
// inside Aotus. Aotus never sees the credentials: they are written by the CLI
// into the profile's configuration directory.
func (c Claude) LoginSession(p Profile) (TerminalSession, error) {
	if !proc.PTYSupported() {
		return nil, fmt.Errorf("%w: the terminal is not available on this platform yet", ErrUnsupportedMode)
	}
	return newTerminalSession(SessionRequest{Profile: p}, fixedLauncher{args: []string{"auth", "login"}}, p.Binary, ProfileEnv(p, c.lookup()), false), nil
}

// claudeFlags are the options shared by every way of starting Claude Code.
func claudeFlags(req SessionRequest) []string {
	var args []string
	if m := firstNonEmpty(req.Model, req.Profile.Model); m != "" {
		args = append(args, "--model", m)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	if req.PermissionMode != "" {
		args = append(args, "--permission-mode", req.PermissionMode)
	}
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowedTools, ","))
	}
	return args
}

// claudeLauncher builds the command lines of the interactive Claude Code. We
// choose the session ID ourselves (--session-id) so that it can be resumed
// later with --resume.
type claudeLauncher struct{}

func (claudeLauncher) New(req SessionRequest, newID string) []string {
	return append([]string{"--session-id", newID}, claudeFlags(req)...)
}

func (claudeLauncher) Resume(req SessionRequest, id string) []string {
	if id == "" {
		return append([]string{"--continue"}, claudeFlags(req)...)
	}
	return append([]string{"--resume", id}, claudeFlags(req)...)
}

// claudeDialect builds `claude -p` turns.
type claudeDialect struct {
	lookup func(string) (string, bool)
}

// Turn implements Dialect. The official binary is started exactly as
// installed, with the prompt on stdin, and nothing that would change how it
// presents itself to Anthropic.
func (d claudeDialect) Turn(t TurnRequest) (Command, Parser, error) {
	p := t.Request.Profile
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages"}
	if t.SessionID != "" {
		args = append(args, "--resume", t.SessionID)
	}
	args = append(args, claudeFlags(t.Request)...)
	return Command{
		Spec: proc.Spec{Path: p.Binary, Args: args, Dir: t.Request.Dir, Env: ProfileEnv(p, d.lookup)},
		// The CLI reads the prompt from stdin; this also keeps it out of the
		// process list.
		Stdin: t.Prompt,
	}, &claudeParser{}, nil
}

// claudeParser reads the stream-json output of one turn.
type claudeParser struct {
	sawDelta map[string]bool // message ID -> text already streamed as deltas
	gotDone  bool
	apiError string // the CLI's own classification of a failed request, if any
}

type claudeLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	Event *struct {
		Type  string `json:"type"`
		Delta *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	APIMessageID string `json:"api_message_id"`

	// Error and IsAPIError mark an assistant line that is really a failure
	// message written by the CLI (for example "authentication_failed").
	Error      string `json:"error"`
	IsAPIError bool   `json:"is_api_error_message"`

	Message *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	RateLimit *struct {
		Status    string `json:"status"`
		ResetsAt  int64  `json:"resetsAt"`
		LimitType string `json:"rateLimitType"`
	} `json:"rate_limit_info"`

	IsError        bool    `json:"is_error"`
	Result         string  `json:"result"`
	TerminalReason string  `json:"terminal_reason"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	Usage          *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Parse implements Parser. Unknown event types, unknown fields and lines that
// are not JSON are ignored: the format depends on the CLI version and on the
// user's own configuration (hooks show up in the stream).
func (c *claudeParser) Parse(l proc.Line) []Event {
	if l.Stream != proc.Stdout || !strings.HasPrefix(strings.TrimSpace(l.Text), "{") {
		return nil
	}
	var m claudeLine
	if json.Unmarshal([]byte(l.Text), &m) != nil {
		return nil
	}
	switch m.Type {
	case "system":
		if m.Subtype == "init" && m.SessionID != "" {
			return []Event{{Kind: EventSession, SessionID: m.SessionID}}
		}
	case "stream_event":
		if m.Event != nil && m.Event.Type == "content_block_delta" && m.Event.Delta != nil &&
			m.Event.Delta.Type == "text_delta" && m.Event.Delta.Text != "" {
			if c.sawDelta == nil {
				c.sawDelta = map[string]bool{}
			}
			c.sawDelta[m.APIMessageID] = true
			return []Event{{Kind: EventText, Text: m.Event.Delta.Text}}
		}
	case "assistant":
		return c.assistant(m)
	case "user":
		return c.userBlocks(m)
	case "rate_limit_event":
		if m.RateLimit != nil {
			return []Event{{Kind: EventLimits, Limits: &Limits{
				Status:   m.RateLimit.Status,
				Window:   m.RateLimit.LimitType,
				ResetsAt: time.Unix(m.RateLimit.ResetsAt, 0).UTC(),
			}}}
		}
	case "result":
		return c.result(m)
	}
	return nil
}

func (c *claudeParser) blocks(m claudeLine) []claudeBlock {
	if m.Message == nil {
		return nil
	}
	var blocks []claudeBlock
	if json.Unmarshal(m.Message.Content, &blocks) != nil {
		return nil // content can also be a plain string
	}
	return blocks
}

func (c *claudeParser) assistant(m claudeLine) []Event {
	if m.Error != "" || m.IsAPIError {
		// A failure notice, not an answer: the result event reports it as an
		// error, so it must not also show up as assistant text.
		c.apiError = m.Error
		return nil
	}
	var out []Event
	id := ""
	if m.Message != nil {
		id = m.Message.ID
	}
	for _, b := range c.blocks(m) {
		switch b.Type {
		case "text":
			// Deltas already delivered this text; only fall back to the full
			// message when the CLI streamed none for it.
			if !c.sawDelta[id] && b.Text != "" {
				out = append(out, Event{Kind: EventText, Text: b.Text})
			}
		case "tool_use":
			out = append(out, Event{Kind: EventToolRequest, Tool: &Tool{ID: b.ID, Name: b.Name, Input: b.Input}})
		}
	}
	return out
}

func (c *claudeParser) userBlocks(m claudeLine) []Event {
	var out []Event
	for _, b := range c.blocks(m) {
		if b.Type == "tool_result" && b.ToolUseID != "" {
			out = append(out, Event{Kind: EventToolResult, Tool: &Tool{ID: b.ToolUseID, Output: toolOutput(b.Content), IsError: b.IsError}})
		}
	}
	return out
}

// toolOutput flattens a tool result's content, which is a string or a list of
// text blocks.
func toolOutput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []claudeBlock
	if json.Unmarshal(raw, &parts) == nil {
		var texts []string
		for _, p := range parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func (c *claudeParser) result(m claudeLine) []Event {
	c.gotDone = true
	done := &Done{CostUSD: m.TotalCostUSD}
	if m.Usage != nil {
		done.InputTokens, done.OutputTokens = m.Usage.InputTokens, m.Usage.OutputTokens
	}
	switch {
	case strings.HasPrefix(m.TerminalReason, "aborted"):
		// Ctrl+C or a cancel: not a failure.
		done.Reason = DoneCanceled
		return []Event{{Kind: EventDone, Done: done}}
	case m.IsError || m.Subtype != "success":
		done.Reason = DoneFailed
		msg := strings.TrimSpace(m.Result)
		if msg == "" {
			msg = "Claude Code reported an error (" + firstNonEmpty(m.Subtype, "unknown") + ")"
		}
		code := classifyClaudeError(msg)
		if byKind := claudeErrorKinds[c.apiError]; byKind != "" {
			code = byKind // the CLI's own classification beats guessing from text
		}
		return []Event{
			{Kind: EventError, Code: code, Text: msg},
			{Kind: EventDone, Done: done},
		}
	}
	done.Reason = DoneCompleted
	return []Event{{Kind: EventDone, Done: done}}
}

// claudeErrorKinds maps the error kinds the CLI attaches to failure notices.
var claudeErrorKinds = map[string]ErrorCode{
	"authentication_failed": CodeNeedsLogin,
	"rate_limit":            CodeRateLimited,
}

// classifyClaudeError maps the CLI's wording to a code the UI can act on.
// These are heuristics over human text and may need updating with the CLI.
func classifyClaudeError(msg string) ErrorCode {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "/login"), strings.Contains(l, "not logged in"), strings.Contains(l, "invalid api key"),
		strings.Contains(l, "authentication"), strings.Contains(l, "unauthorized"), strings.Contains(l, "token has expired"):
		return CodeNeedsLogin
	case strings.Contains(l, "usage limit"), strings.Contains(l, "rate limit"), strings.Contains(l, "limit reached"), strings.Contains(l, "too many requests"):
		return CodeRateLimited
	case strings.Contains(l, "model") && (strings.Contains(l, "not supported") || strings.Contains(l, "not found") || strings.Contains(l, "does not exist")):
		return CodeModelUnsupported
	}
	return CodeCLI
}

// OnExit implements Parser: a CLI that died without a result event usually
// said why on stderr.
func (c *claudeParser) OnExit(exit proc.Exit, recent []proc.Line) []Event {
	if c.gotDone || exit.Code == 0 || exit.Canceled {
		return nil
	}
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Stream != proc.Stderr || strings.TrimSpace(recent[i].Text) == "" {
			continue
		}
		msg := strings.TrimSpace(recent[i].Text)
		if code := classifyClaudeError(msg); code != CodeCLI {
			return []Event{{Kind: EventError, Code: code, Text: msg}}
		}
		return nil // the shared runner reports the generic exit
	}
	return nil
}
