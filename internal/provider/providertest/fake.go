package providertest

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"aotus/internal/proc"
	"aotus/internal/provider"
)

// KindFake is the kind of the fake provider.
const KindFake provider.Kind = "fake"

// Fake is a provider whose "CLI" is the test binary running a scenario. It is
// built on the shared process session, so it exercises the same code real
// adapters use.
type Fake struct {
	Scenario Scenario
	// PidFile is where the Tree scenario records "leader grandchild" PIDs.
	PidFile string
}

// Kind implements provider.Provider.
func (Fake) Kind() provider.Kind { return KindFake }

// Detect implements provider.Provider.
func (Fake) Detect(context.Context, provider.Profile) (provider.Detection, error) {
	return provider.Detection{
		Installed: true,
		Version:   "fake 1.0",
		VersionOK: true,
		Modes:     []provider.Mode{provider.ModeStructured},
		Login:     provider.LoginLoggedIn,
	}, nil
}

// Start implements provider.Provider.
func (f Fake) Start(_ context.Context, req provider.SessionRequest) (provider.Session, error) {
	if req.Mode != "" && req.Mode != provider.ModeStructured {
		return nil, provider.ErrUnsupportedMode
	}
	return provider.NewProcessSession(provider.ModeStructured, fakeDialect{f: f}, req), nil
}

type fakeDialect struct{ f Fake }

func (d fakeDialect) Turn(t provider.TurnRequest) (provider.Command, provider.Parser, error) {
	// Use the real environment builder, so the contract tests exercise the
	// allow-list and not a hand-written copy of it.
	prof := t.Request.Profile
	prof.ExtraEnv = map[string]string{EnvFakeCLI: "1"}
	env := provider.ProfileEnv(prof, provider.OSEnv)
	return provider.Command{Spec: proc.Spec{
		Path: os.Args[0],
		Args: []string{
			"--scenario", string(d.f.Scenario),
			"--pidfile", d.f.PidFile,
			"--resume", t.SessionID,
			"--prompt", t.Prompt,
		},
		Dir:   t.Request.Dir,
		Env:   env,
		Grace: 300 * time.Millisecond,
	}}, fakeParser{}, nil
}

type fakeParser struct{}

func (fakeParser) Parse(l proc.Line) []provider.Event {
	if l.Stream != proc.Stdout {
		return nil
	}
	var m struct {
		Type         string `json:"type"`
		ID           string `json:"id"`
		Text         string `json:"text"`
		Code         string `json:"code"`
		Message      string `json:"message"`
		Reason       string `json:"reason"`
		InputTokens  int    `json:"input_tokens"`
		OutputTokens int    `json:"output_tokens"`
	}
	if json.Unmarshal([]byte(l.Text), &m) != nil {
		return nil // tolerate lines that are not ours
	}
	switch m.Type {
	case "session":
		return []provider.Event{{Kind: provider.EventSession, SessionID: m.ID}}
	case "text":
		return []provider.Event{{Kind: provider.EventText, Text: m.Text}}
	case "error":
		return []provider.Event{{Kind: provider.EventError, Code: provider.ErrorCode(m.Code), Text: m.Message}}
	case "done":
		return []provider.Event{{Kind: provider.EventDone, Done: &provider.Done{
			Reason:       provider.DoneReason(m.Reason),
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
		}}}
	}
	return nil
}

func (fakeParser) OnExit(proc.Exit, []proc.Line) []provider.Event { return nil }
