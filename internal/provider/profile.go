package provider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"aotus/internal/datadir"
)

// Known provider kinds.
const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"
	KindOpenAI Kind = "openai-api"
)

// Profile is how to reach one subscription (ADR 0008). It holds no secrets:
// the login lives inside the CLI's own configuration directory, which Aotus
// never opens, and an API key is only referenced by name in the operating
// system credential store.
type Profile struct {
	ID        string
	Name      string
	Kind      Kind
	Binary    string // path of the official CLI
	ConfigDir string // isolated configuration directory of this profile
	Mode      Mode   // default integration mode
	Model     string // optional model override

	// ExtraEnv is non-secret static configuration for the CLI, for example a
	// proxy. Names that look like credentials are rejected.
	ExtraEnv map[string]string
	// APIKeyRef names the entry in the operating system credential store that
	// holds the API key (api mode). It is never the key itself.
	APIKeyRef string
	// AcceptedNotices lists the notices the user accepted for this profile (for
	// example NoticeClaudeHeadless). A mode guarded by a notice refuses to
	// start without it.
	AcceptedNotices []string
	// TermsCheckedAt is the date (YYYY-MM-DD) the provider's terms were last
	// reviewed for this integration mode (docs/research/provider-terms.md).
	TermsCheckedAt string
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// secretName matches environment variable names that look like credentials.
var secretName = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|cookie|credential|auth)`)

// NewProfile creates a profile with a new, unique configuration directory
// under the data directory. Two calls never share a directory, even with the
// same name. The directory itself is created by EnsureConfigDir.
func NewProfile(l datadir.Layout, kind Kind, name, binary string) (Profile, error) {
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		return Profile{}, fmt.Errorf("provider: profile name %q has no usable characters", name)
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Profile{}, fmt.Errorf("provider: generating a profile ID: %w", err)
	}
	id := string(kind) + "-" + slug + "-" + hex.EncodeToString(b[:])
	return Profile{
		ID:        id,
		Name:      name,
		Kind:      kind,
		Binary:    binary,
		ConfigDir: l.ProfileDir(id),
	}, nil
}

// EnsureConfigDir creates the profile's configuration directory (owner-only).
func (p Profile) EnsureConfigDir() error {
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("provider: creating %s: %w", p.ConfigDir, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p.ConfigDir, 0o700); err != nil { //nolint:gosec // directories need the execute bit; 0700 is owner-only
			return fmt.Errorf("provider: restricting %s: %w", p.ConfigDir, err)
		}
	}
	return nil
}

// Validate checks one profile. The configuration directory must be inside the
// data directory's profiles folder: pointing a profile at the CLI's default
// location (for example ~/.claude) would silently share the user's own login.
func (p Profile) Validate(l datadir.Layout) error {
	switch {
	case !idPattern.MatchString(p.ID):
		return fmt.Errorf("provider: profile ID %q must be lowercase letters, digits, - or _", p.ID)
	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("provider: profile %s needs a name", p.ID)
	case p.Kind == "":
		return fmt.Errorf("provider: profile %s needs a kind", p.ID)
	case p.Binary == "" && p.Kind != KindOpenAI:
		return fmt.Errorf("provider: profile %s needs the path of the CLI", p.ID)
	case p.ConfigDir == "":
		return fmt.Errorf("provider: profile %s needs its own configuration directory", p.ID)
	}
	if !within(l.ProfilesDir(), p.ConfigDir) {
		return fmt.Errorf("provider: profile %s: configuration directory %s must be inside %s", p.ID, p.ConfigDir, l.ProfilesDir())
	}
	for name := range p.ExtraEnv {
		if secretName.MatchString(name) {
			return fmt.Errorf("provider: profile %s: environment variable %s looks like a credential; profiles hold no secrets", p.ID, name)
		}
	}
	return nil
}

// CheckIsolation verifies that no two profiles share, or nest, their
// configuration directories.
func CheckIsolation(profiles []Profile) error {
	for i, a := range profiles {
		for _, b := range profiles[i+1:] {
			if within(a.ConfigDir, b.ConfigDir) || within(b.ConfigDir, a.ConfigDir) {
				return fmt.Errorf("provider: profiles %s and %s share state: %s and %s overlap", a.ID, b.ID, a.ConfigDir, b.ConfigDir)
			}
		}
	}
	return nil
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// passThrough lists the variables of the daemon's environment a child may
// see: what any program needs to run and to reach the network. Everything else
// is dropped, in particular API keys and tokens, which could silently switch a
// CLI from the user's subscription to pay-per-token billing.
var passThrough = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ",
	"TMPDIR", "TEMP", "TMP",
	"USERPROFILE", "SYSTEMROOT", "COMSPEC", "PATHEXT", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// configEnvVar is the variable each CLI reads to find its configuration
// directory (verified in docs/research/cli-contracts.md).
var configEnvVar = map[Kind]string{
	KindClaude: "CLAUDE_CONFIG_DIR",
	KindCodex:  "CODEX_HOME",
}

// ProfileEnv builds the complete environment of a child process for this
// profile: the allow-listed variables found by lookup, the profile's
// configuration directory variable, and the profile's own ExtraEnv. Nothing
// else is inherited.
func ProfileEnv(p Profile, lookup func(string) (string, bool)) []string {
	env := map[string]string{}
	for _, name := range passThrough {
		if v, ok := lookup(name); ok {
			env[name] = v
		}
	}
	for k, v := range p.ExtraEnv {
		env[k] = v
	}
	if name, ok := configEnvVar[p.Kind]; ok && p.ConfigDir != "" {
		env[name] = p.ConfigDir
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// OSEnv is the lookup that reads the daemon's real environment.
func OSEnv(name string) (string, bool) { return os.LookupEnv(name) }
