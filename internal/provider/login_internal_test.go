package provider

import (
	"os"
	"strings"
	"testing"
)

// The recorded outputs of the real CLIs (testdata/README.md) must parse.
func TestParseLoginFromRecordedOutput(t *testing.T) {
	read := func(name string) []string {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
	cases := []struct {
		kind   Kind
		file   string
		state  LoginState
		method string
		plan   string
	}{
		{KindClaude, "claude-auth-status-logged-in.json", LoginLoggedIn, "claude.ai", "pro"},
		{KindClaude, "claude-auth-status-logged-out.json", LoginLoggedOut, "", ""},
		{KindCodex, "codex-login-status-logged-in.txt", LoginLoggedIn, "ChatGPT", ""},
		{KindCodex, "codex-login-status-logged-out.txt", LoginLoggedOut, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, err := parseLogin(tc.kind, read(tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.state || got.Method != tc.method || got.Plan != tc.plan {
				t.Fatalf("got %+v, want state %s method %q plan %q", got, tc.state, tc.method, tc.plan)
			}
		})
	}
	if _, err := parseLogin(KindClaude, []string{"not json"}); err == nil {
		t.Fatal("garbage must be an error, not a guess")
	}
	if got, err := parseLogin(KindCodex, []string{"WARNING: something", "", "Logged in using ChatGPT", ""}); err != nil || got.State != LoginLoggedIn {
		t.Fatalf("only the last line counts: %+v, %v", got, err)
	}
	if _, err := parseLogin(KindCodex, []string{"Something unexpected"}); err == nil {
		t.Fatal("an unrecognized codex status must be an error")
	}
}
