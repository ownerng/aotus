package provider

import (
	"encoding/json"
	"time"
)

// EventKind says what an Event carries.
type EventKind string

// Event kinds. Every provider's output is normalized to these.
const (
	// EventSession reports the provider's session ID once it is known.
	EventSession EventKind = "session"
	// EventText is a piece of the assistant's answer.
	EventText EventKind = "text"
	// EventToolRequest means the assistant wants to use a tool.
	EventToolRequest EventKind = "tool_request"
	// EventToolResult is the outcome of a tool use.
	EventToolResult EventKind = "tool_result"
	// EventLimits reports the state of the plan's usage limits.
	EventLimits EventKind = "limits"
	// EventError reports a problem during the turn. A failed turn has at
	// least one before its Done event.
	EventError EventKind = "error"
	// EventDone ends a turn. It is always the last event of the turn.
	EventDone EventKind = "done"
)

// ErrorCode classifies an EventError so the UI can react (for example by
// offering to log in again) without parsing text.
type ErrorCode string

// Error codes.
const (
	CodeNeedsLogin         ErrorCode = "needs_login"
	CodeRateLimited        ErrorCode = "rate_limited"
	CodeModelUnsupported   ErrorCode = "model_unsupported"
	CodeUnsupportedVersion ErrorCode = "unsupported_version"
	// CodeCLI is a failure reported by the CLI itself.
	CodeCLI ErrorCode = "cli_error"
	// CodeProtocol means the output could not be understood.
	CodeProtocol ErrorCode = "protocol"
	CodeInternal ErrorCode = "internal"
)

// DoneReason says why a turn ended.
type DoneReason string

// Reasons a turn ends.
const (
	DoneCompleted DoneReason = "completed"
	DoneCanceled  DoneReason = "canceled"
	DoneFailed    DoneReason = "failed"
)

// Event is one normalized fact about a turn. Events never carry credentials
// or environment variables.
type Event struct {
	Kind      EventKind
	SessionID string // EventSession
	Text      string // EventText: the text; EventError: the message
	Tool      *Tool  // EventToolRequest, EventToolResult
	Limits    *Limits
	Code      ErrorCode // EventError
	Done      *Done     // EventDone
}

// Tool describes a tool use.
type Tool struct {
	ID      string
	Name    string
	Input   json.RawMessage // request
	Output  string          // result
	IsError bool            // result
}

// Limits is the plan limit state reported by a provider.
type Limits struct {
	Status   string // for example "allowed"
	Window   string // for example "five_hour"
	ResetsAt time.Time
}

// Done closes a turn.
type Done struct {
	Reason       DoneReason
	CostUSD      float64
	InputTokens  int
	OutputTokens int
}
