package provider

import (
	"context"
	"errors"
	"io"
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

// Profile is how to reach one subscription: which CLI, with which isolated
// configuration directory. It holds no secrets (ADR 0008).
type Profile struct {
	ID        string
	Name      string
	Kind      Kind
	Binary    string // path of the official CLI
	ConfigDir string // isolated configuration directory of this profile
	Mode      Mode
	Model     string // optional model override
}

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

// Terminal is the raw side of a ModeTerminal session: bytes to and from the
// pseudo-terminal that hosts the official interactive UI.
type Terminal interface {
	io.ReadWriteCloser
	Resize(rows, cols int) error
}
