package provider_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"aotus/internal/datadir"
	"aotus/internal/proc"
	"aotus/internal/provider"
)

func layout(t *testing.T) datadir.Layout {
	t.Helper()
	l := datadir.Layout{Root: filepath.Join(t.TempDir(), "aotus")}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	return l
}

// fakeProfile is a profile whose "CLI" is the fake one (the test binary).
func fakeProfile(t *testing.T, l datadir.Layout, kind provider.Kind, name, login string) provider.Profile {
	t.Helper()
	p, err := provider.NewProfile(l, kind, name, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	p.ExtraEnv = map[string]string{"AOTUS_FAKE_CLI": "1"}
	if err := p.EnsureConfigDir(); err != nil {
		t.Fatal(err)
	}
	if login != "" {
		if err := os.WriteFile(filepath.Join(p.ConfigDir, "fake-login"), []byte(login), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func noEnv(string) (string, bool) { return "", false }

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestProfileUsesIsolatedConfigDir(t *testing.T) {
	l := layout(t)
	claude, err := provider.NewProfile(l, provider.KindClaude, "Work account", "/usr/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := provider.NewProfile(l, provider.KindCodex, "Personal", "/usr/bin/codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []provider.Profile{claude, codex} {
		if err := p.Validate(l); err != nil {
			t.Fatalf("a new profile must be valid: %v", err)
		}
		if !strings.HasPrefix(p.ConfigDir, l.ProfilesDir()+string(filepath.Separator)) {
			t.Fatalf("config dir %s must live under %s", p.ConfigDir, l.ProfilesDir())
		}
		if err := p.EnsureConfigDir(); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p.ConfigDir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("config dir was not created: %v", err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Fatalf("config dir mode = %o, want 700", fi.Mode().Perm())
		}
	}
	if got := envMap(provider.ProfileEnv(claude, noEnv))["CLAUDE_CONFIG_DIR"]; got != claude.ConfigDir {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the profile's directory %q", got, claude.ConfigDir)
	}
	if got := envMap(provider.ProfileEnv(codex, noEnv))["CODEX_HOME"]; got != codex.ConfigDir {
		t.Fatalf("CODEX_HOME = %q, want the profile's directory %q", got, codex.ConfigDir)
	}

	home, _ := os.UserHomeDir()
	for name, dir := range map[string]string{
		"the CLI's default location": filepath.Join(home, ".claude"),
		"a path that climbs out":     filepath.Join(l.ProfilesDir(), "..", "elsewhere"),
		"the profiles folder itself": filepath.Join(l.ProfilesDir(), "..", "profiles", ".."),
		"nothing":                    "",
	} {
		bad := claude
		bad.ConfigDir = dir
		if err := bad.Validate(l); err == nil {
			t.Fatalf("a profile pointing at %s must be rejected: it would share the user's own login", name)
		}
	}
}

func TestTwoProfilesOfSameProviderDoNotShareState(t *testing.T) {
	l := layout(t)
	a, _ := provider.NewProfile(l, provider.KindClaude, "Same name", os.Args[0])
	b, _ := provider.NewProfile(l, provider.KindClaude, "Same name", os.Args[0])
	if a.ID == b.ID || a.ConfigDir == b.ConfigDir {
		t.Fatalf("two profiles with the same name must still get different IDs and directories: %+v %+v", a, b)
	}
	if err := provider.CheckIsolation([]provider.Profile{a, b}); err != nil {
		t.Fatalf("separate profiles must pass: %v", err)
	}

	shared := b
	shared.ConfigDir = a.ConfigDir
	nested := b
	nested.ConfigDir = filepath.Join(a.ConfigDir, "inside")
	for name, other := range map[string]provider.Profile{"same directory": shared, "nested directory": nested} {
		if err := provider.CheckIsolation([]provider.Profile{a, other}); err == nil {
			t.Fatalf("%s must be reported as shared state", name)
		}
	}

	// Behavior, not just paths: one profile logged in, the other not.
	in := fakeProfile(t, l, provider.KindClaude, "logged in", "in")
	out := fakeProfile(t, l, provider.KindClaude, "logged out", "out")
	li, err := provider.CheckLogin(context.Background(), in, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	lo, err := provider.CheckLogin(context.Background(), out, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if li.State != provider.LoginLoggedIn || lo.State != provider.LoginLoggedOut {
		t.Fatalf("each profile must keep its own login: got %s and %s", li.State, lo.State)
	}
}

func TestLoginStatusDetectedWithoutReadingCredentialFiles(t *testing.T) {
	l := layout(t)
	ctx := context.Background()

	claude := fakeProfile(t, l, provider.KindClaude, "claude", "in")
	canary := filepath.Join(claude.ConfigDir, ".credentials.json")
	if err := os.WriteFile(canary, []byte(`{"token":"CANARY-DO-NOT-READ"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// Make the credential file unreadable: a daemon that tried to open it
		// would fail (the check is moot when running as root, so the static
		// check below also guards this).
		if err := os.Chmod(canary, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(canary, 0o600) })
	}
	login, err := provider.CheckLogin(ctx, claude, noEnv)
	if err != nil {
		t.Fatalf("login detection must not depend on reading credentials: %v", err)
	}
	if login.State != provider.LoginLoggedIn || login.Method != "claude.ai" || login.Plan != "pro" {
		t.Fatalf("login = %+v, want logged in with claude.ai and pro", login)
	}

	// Personal data that the CLI prints must not be kept.
	if v := reflect.TypeOf(login); v.NumField() != 3 {
		t.Fatalf("Login has %d fields; it may only hold state, method and plan, never an e-mail or organization", v.NumField())
	}

	if err := os.WriteFile(filepath.Join(claude.ConfigDir, "fake-login"), []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}
	if login, _ := provider.CheckLogin(ctx, claude, noEnv); login.State != provider.LoginLoggedOut {
		t.Fatalf("login = %+v, want logged out", login)
	}

	codex := fakeProfile(t, l, provider.KindCodex, "codex", "in")
	login, err = provider.CheckLogin(ctx, codex, noEnv)
	if err != nil || login.State != provider.LoginLoggedIn || login.Method != "ChatGPT" {
		t.Fatalf("codex login = %+v, %v; want logged in using ChatGPT (the stderr warning must be ignored)", login, err)
	}

	missing := claude
	missing.Binary = filepath.Join(t.TempDir(), "no-such-claude")
	if login, err := provider.CheckLogin(ctx, missing, noEnv); !errors.Is(err, proc.ErrBinaryNotFound) || login.State != provider.LoginUnknown {
		t.Fatalf("missing CLI: login = %+v, err = %v; want unknown and ErrBinaryNotFound", login, err)
	}

	assertProviderNeverOpensFiles(t)
}

// assertProviderNeverOpensFiles is the structural guarantee behind the canary:
// the provider package's own code contains no call that opens, reads or lists
// files, so it cannot read a CLI's credentials. Creating the configuration
// directory (MkdirAll, Chmod) is allowed.
func assertProviderNeverOpensFiles(t *testing.T) {
	t.Helper()
	forbidden := map[string]bool{
		"os.Open": true, "os.OpenFile": true, "os.ReadFile": true, "os.ReadDir": true, "os.Create": true,
		"ioutil.ReadFile": true, "ioutil.ReadDir": true, "filepath.Walk": true, "filepath.WalkDir": true,
		"fs.ReadFile": true, "fs.ReadDir": true, "fs.WalkDir": true, "os.DirFS": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("cannot list the package sources: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && forbidden[pkg.Name+"."+sel.Sel.Name] {
				t.Errorf("%s: %s.%s: the provider package must not open or read files", fset.Position(call.Pos()), pkg.Name, sel.Sel.Name)
			}
			return true
		})
	}
}

func TestChildEnvironmentIsAllowListed(t *testing.T) {
	daemon := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/u", "LANG": "en_US.UTF-8", "HTTPS_PROXY": "http://proxy:3128",
		"ANTHROPIC_API_KEY": "sk-ant-secret", "OPENAI_API_KEY": "sk-secret", "GITHUB_TOKEN": "ghp_secret",
		"AWS_SECRET_ACCESS_KEY": "aws-secret", "SSH_AUTH_SOCK": "/run/ssh", "CLAUDE_CONFIG_DIR": "/home/u/.claude",
		"CODEX_HOME": "/home/u/.codex", "RANDOM_THING": "x",
	}
	lookup := func(name string) (string, bool) { v, ok := daemon[name]; return v, ok }

	p := provider.Profile{ID: "claude-x-1", Kind: provider.KindClaude, ConfigDir: "/data/profiles/claude-x-1",
		ExtraEnv: map[string]string{"NO_PROXY": "localhost"}}
	got := envMap(provider.ProfileEnv(p, lookup))

	want := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/u", "LANG": "en_US.UTF-8", "HTTPS_PROXY": "http://proxy:3128",
		"NO_PROXY": "localhost", "CLAUDE_CONFIG_DIR": "/data/profiles/claude-x-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %v\nwant exactly      %v\n(API keys and tokens of the daemon must never reach the CLI: an API key would silently replace the subscription)", got, want)
	}

	if !sort.StringsAreSorted(provider.ProfileEnv(p, lookup)) {
		t.Fatal("the environment must be deterministic (sorted)")
	}

	// The profile's own directory wins over any value in the daemon's environment.
	cx := provider.Profile{ID: "codex-x-1", Kind: provider.KindCodex, ConfigDir: "/data/profiles/codex-x-1"}
	if e := envMap(provider.ProfileEnv(cx, lookup)); e["CODEX_HOME"] != "/data/profiles/codex-x-1" || e["CLAUDE_CONFIG_DIR"] != "" {
		t.Fatalf("codex environment = %v", e)
	}
}

func TestProfileHoldsNoSecrets(t *testing.T) {
	forbidden := regexpForSecrets()
	rt := reflect.TypeOf(provider.Profile{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if name == "APIKeyRef" {
			continue // a reference to the OS credential store, never the key
		}
		if forbidden(name) {
			t.Errorf("Profile.%s looks like a place for a secret; profiles hold none", name)
		}
	}

	l := layout(t)
	p := fakeProfile(t, l, provider.KindClaude, "ok", "")
	for _, name := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "DB_PASSWORD", "SESSION_COOKIE", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
		bad := p
		bad.ExtraEnv = map[string]string{name: "x"}
		if err := bad.Validate(l); err == nil {
			t.Errorf("ExtraEnv %s must be rejected: it looks like a credential", name)
		}
	}
	good := p
	good.ExtraEnv = map[string]string{"HTTPS_PROXY": "http://proxy:3128"}
	if err := good.Validate(l); err != nil {
		t.Errorf("a proxy is plain configuration and must be accepted: %v", err)
	}
}

func regexpForSecrets() func(string) bool {
	words := []string{"key", "token", "secret", "password", "passwd", "cookie", "credential"}
	return func(name string) bool {
		n := strings.ToLower(name)
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
}

func TestNoAutomaticProfileRotation(t *testing.T) {
	banned := []string{"profile", "rotate", "rotation", "switch", "failover", "fallback", "pool", "balance"}
	for _, iface := range []reflect.Type{reflect.TypeOf((*provider.Session)(nil)).Elem(), reflect.TypeOf((*provider.Provider)(nil)).Elem()} {
		for i := 0; i < iface.NumMethod(); i++ {
			name := strings.ToLower(iface.Method(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: the API must not offer a way to change profile on its own (ADR 0008)", iface.Name(), iface.Method(i).Name)
				}
			}
		}
	}
	// A session is bound to exactly one profile, chosen by the user.
	f, ok := reflect.TypeOf(provider.SessionRequest{}).FieldByName("Profile")
	if !ok || f.Type != reflect.TypeOf(provider.Profile{}) {
		t.Fatalf("SessionRequest.Profile must be a single Profile, got %v", f.Type)
	}
}

func TestVersionComparison(t *testing.T) {
	if v, ok := provider.ParseVersion("2.1.295 (Claude Code)"); !ok || v != "2.1.295" {
		t.Fatalf("ParseVersion = %q, %v", v, ok)
	}
	if v, ok := provider.ParseVersion("codex-cli 0.160.0"); !ok || v != "0.160.0" {
		t.Fatalf("ParseVersion = %q, %v", v, ok)
	}
	if _, ok := provider.ParseVersion("no version here"); ok {
		t.Fatal("text without a version must not parse")
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{{"2.1.295", "2.1.295", 0}, {"2.1.294", "2.1.295", -1}, {"2.2.0", "2.1.999", 1}, {"10.0.0", "9.9.9", 1}, {"0.160.0", "0.160.1", -1}} {
		if got := provider.CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
