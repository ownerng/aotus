package provider

import (
	"context"
	"errors"
)

// Kind identifies a provider implementation.
type Kind string

// Mode is how Aotus talks to a CLI (ADR 0008).
type Mode string

// Integration modes.
const (
	// ModeStructured uses the CLI's headless or protocol mode with streamed
	// JSON, one process per turn.
	ModeStructured Mode = "structured"
	// ModeTerminal hosts the official interactive UI in a pseudo-terminal.
	ModeTerminal Mode = "terminal"
	// ModeAPI talks to an OpenAI-compatible endpoint with the user's key.
	ModeAPI Mode = "api"
)

// Errors returned by sessions. Use errors.Is.
var (
	// ErrTurnActive means a turn is already running in this session.
	ErrTurnActive = errors.New("a turn is already running in this session")
	// ErrSessionClosed means the session was closed.
	ErrSessionClosed = errors.New("session is closed")
	// ErrNoTurn means there is no running turn to act on.
	ErrNoTurn = errors.New("no turn is running")
	// ErrUnsupportedMode means the provider cannot run in the requested mode.
	ErrUnsupportedMode = errors.New("mode is not supported by this provider")
)

// LoginState is what a provider can tell about the login of a profile.
type LoginState string

// Login states.
const (
	LoginUnknown   LoginState = "unknown"
	LoginLoggedIn  LoginState = "logged_in"
	LoginLoggedOut LoginState = "logged_out"
)

// Detection is the result of looking at the CLI of a profile.
type Detection struct {
	Installed bool
	Version   string
	// VersionOK is false when the CLI is older than the minimum tested one.
	VersionOK bool
	Modes     []Mode
	Login     LoginState
	// Detail is a short human explanation for the UI when something is wrong.
	Detail string
}

// SessionRequest asks a provider for a session.
type SessionRequest struct {
	Profile      Profile
	Mode         Mode
	Dir          string // working directory (the employee's folder)
	SystemPrompt string
	ResumeID     string // provider session to continue, if any
	Model        string // overrides Profile.Model
	// PermissionMode and AllowedTools are passed to CLIs that support them;
	// they come from the employee's permissions.
	PermissionMode string
	AllowedTools   []string
	// ExtraEnv is added to the profile's environment (for example the
	// employee's private temporary directory). It wins over the profile's.
	ExtraEnv []string
}

// Provider adapts one family of CLIs (or an API) to Aotus.
type Provider interface {
	Kind() Kind
	// Detect inspects the CLI of the profile: installed, version, supported
	// modes and login status. It never reads credential files.
	Detect(ctx context.Context, p Profile) (Detection, error)
	// Start creates a session. No process runs until the first Send.
	Start(ctx context.Context, req SessionRequest) (Session, error)
}

// Session is a conversation with an employee's CLI. In structured mode each
// Send runs one supervised process; the conversation continues through the
// provider's own session ID.
type Session interface {
	Mode() Mode
	// ID is the provider's session ID, empty until the first turn reports it.
	ID() string
	// Events delivers normalized events of all turns, in order. It is closed
	// when the session is closed. Each turn ends with exactly one EventDone.
	Events() <-chan Event
	// Send starts a turn. It returns ErrTurnActive if one is running.
	Send(ctx context.Context, prompt string) error
	// Interrupt asks the running turn to stop politely (like Ctrl+C); the turn
	// still ends with an EventDone. It falls back to CancelTurn where the
	// platform has no polite interrupt.
	Interrupt() error
	// CancelTurn ends the running turn immediately, killing its whole process
	// tree. The session stays usable.
	CancelTurn() error
	// Close ends the session and everything it started.
	Close() error
	// Done is closed once the session is fully closed.
	Done() <-chan struct{}
}

// Terminal is the raw side of a ModeTerminal session: the pseudo-terminal that
// hosts the official interactive UI. It outlives any viewer.
type Terminal interface {
	// Subscribe returns the recent output and a channel with everything that
	// follows. The channel closes when the program ends or when the viewer
	// falls too far behind (subscribe again to resume with a fresh replay).
	Subscribe() (replay []byte, live <-chan []byte, cancel func())
	// Write sends input, as if typed.
	Write(p []byte) (int, error)
	// Resize changes the size; the program is told through SIGWINCH.
	Resize(rows, cols uint16) error
}

// TerminalSession is a Session in ModeTerminal. The program runs from Launch
// until it ends or the session is cancelled or closed; it keeps running with
// no viewer attached. Events carries the session ID once known and one final
// EventDone when the program ends; the terminal output itself is not turned
// into events, it is the terminal's.
type TerminalSession interface {
	Session
	// Launch starts the official program in a pseudo-terminal of this size. If
	// the session ran before it resumes the provider session. It returns
	// ErrTurnActive while the program is already running. ctx only bounds the
	// start: the program does not stop when ctx ends.
	Launch(ctx context.Context, rows, cols uint16) error
	// Terminal is the running terminal, or nil when the program is not running.
	Terminal() Terminal
}
