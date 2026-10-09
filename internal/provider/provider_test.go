package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aotus/internal/provider"
	"aotus/internal/provider/providertest"
)

func TestMain(m *testing.M) {
	providertest.MaybeRunFakeCLI()
	os.Exit(m.Run())
}

// fakeContract runs the shared checks against the fake provider.
func fakeContract() providertest.Contract {
	return providertest.Contract{
		New: func(t *testing.T, sc providertest.Scenario) (provider.Session, providertest.Probe) {
			t.Helper()
			pidFile := filepath.Join(t.TempDir(), "pids")
			f := providertest.Fake{Scenario: sc, PidFile: pidFile}
			s, err := f.Start(context.Background(), provider.SessionRequest{Mode: provider.ModeStructured, Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			return s, providertest.Probe{PidFile: pidFile}
		},
	}
}

func TestContractWithFakeProvider(t *testing.T) {
	providertest.RunContract(t, fakeContract())
}

func TestEventsAreNormalized(t *testing.T) {
	providertest.CheckNormalized(t, fakeContract())
}

func TestCancelStopsProcessTree(t *testing.T) {
	providertest.CheckCancelStopsTree(t, fakeContract())
}

func TestEventsNeverLeakEnvironment(t *testing.T) {
	providertest.CheckNoEnvLeak(t, fakeContract())
}

func TestFakeProviderRejectsOtherModes(t *testing.T) {
	_, err := providertest.Fake{Scenario: providertest.Hello}.Start(context.Background(), provider.SessionRequest{Mode: provider.ModeTerminal})
	if err == nil {
		t.Fatal("the fake provider only supports structured mode")
	}
}

func TestValidateTurnRejectsBrokenSequences(t *testing.T) {
	done := func(r provider.DoneReason) provider.Event {
		return provider.Event{Kind: provider.EventDone, Done: &provider.Done{Reason: r}}
	}
	text := provider.Event{Kind: provider.EventText, Text: "hi"}
	errEv := provider.Event{Kind: provider.EventError, Code: provider.CodeCLI, Text: "boom"}
	cases := []struct {
		name   string
		events []provider.Event
		want   string // substring of the error, "" for valid
	}{
		{"valid", []provider.Event{text, done(provider.DoneCompleted)}, ""},
		{"valid failure", []provider.Event{errEv, done(provider.DoneFailed)}, ""},
		{"valid cancel with error", []provider.Event{errEv, done(provider.DoneCanceled)}, ""},
		{"empty", nil, "no events"},
		{"no done", []provider.Event{text}, "exactly one done"},
		{"two dones", []provider.Event{done(provider.DoneCompleted), done(provider.DoneCompleted)}, "after the done"},
		{"event after done", []provider.Event{done(provider.DoneCompleted), text}, "after the done"},
		{"empty text", []provider.Event{{Kind: provider.EventText}, done(provider.DoneCompleted)}, "empty text"},
		{"error without code", []provider.Event{{Kind: provider.EventError, Text: "x"}, done(provider.DoneFailed)}, "code and a message"},
		{"failed without error", []provider.Event{text, done(provider.DoneFailed)}, "without any error"},
		{"completed with error", []provider.Event{errEv, done(provider.DoneCompleted)}, "completed but reported"},
		{"result without request", []provider.Event{{Kind: provider.EventToolResult, Tool: &provider.Tool{ID: "x"}}, done(provider.DoneCompleted)}, "without a matching request"},
		{"unknown kind", []provider.Event{{Kind: "weird"}, done(provider.DoneCompleted)}, "unknown kind"},
		{"two sessions", []provider.Event{{Kind: provider.EventSession, SessionID: "a"}, {Kind: provider.EventSession, SessionID: "b"}, done(provider.DoneCompleted)}, "reported 2 times"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := provider.ValidateTurn(tc.events)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
