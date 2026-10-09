package provider

import (
	"context"
	"errors"
	"fmt"

	"aotus/internal/proc"
)

// Errors a caller can tell apart before starting a session.
var (
	// ErrCLINotInstalled means the profile's binary does not exist.
	ErrCLINotInstalled = errors.New("the CLI is not installed")
	// ErrNeedsLogin means the profile's CLI is not logged in.
	ErrNeedsLogin = errors.New("the CLI is not logged in")
	// ErrUnsupportedVersion means the CLI is older than the oldest tested one.
	ErrUnsupportedVersion = errors.New("the CLI version is older than the oldest tested one")
	// ErrNoticeRequired means the user has not accepted a notice that guards
	// this mode (see Profile.AcceptedNotices).
	ErrNoticeRequired = errors.New("the user must accept a notice before this mode can be used")
)

// detectCLI inspects the official CLI of a profile: whether it is installed,
// its version against the oldest tested one, and its login status. A missing
// CLI is a state of the answer, not an error.
func detectCLI(ctx context.Context, p Profile, display string, modes []Mode, lookup func(string) (string, bool)) (Detection, error) {
	d := Detection{Login: LoginUnknown}
	out, err := runOnce(ctx, p.Binary, []string{"--version"}, ProfileEnv(p, lookup))
	if err != nil {
		if errors.Is(err, proc.ErrBinaryNotFound) || errors.Is(err, proc.ErrNotExecutable) {
			d.Detail = fmt.Sprintf("%s was not found at %q. Install the official CLI or choose its location.", display, p.Binary)
			return d, nil
		}
		return d, err
	}
	d.Installed = true
	if v, ok := ParseVersion(out); ok {
		d.Version = v
		d.VersionOK = CompareVersions(v, MinVersion[p.Kind]) >= 0
	}
	if !d.VersionOK {
		d.Detail = fmt.Sprintf("%s %s is older than %s, the oldest version Aotus was tested with.", display, orUnknown(d.Version), MinVersion[p.Kind])
	}
	d.Modes = modes

	login, err := CheckLogin(ctx, p, lookup)
	if err != nil {
		if d.Detail == "" {
			d.Detail = "Could not read the login status: " + err.Error()
		}
		return d, nil
	}
	d.Login = login.State
	if login.State == LoginLoggedOut && d.Detail == "" {
		d.Detail = fmt.Sprintf("This profile is not logged in. Log in with %s using this profile.", display)
	}
	return d, nil
}

// Err turns a detection into one of the typed errors above, or nil when the
// CLI can be used.
func (d Detection) Err() error {
	switch {
	case !d.Installed:
		return fmt.Errorf("%w: %s", ErrCLINotInstalled, d.Detail)
	case !d.VersionOK:
		return fmt.Errorf("%w: %s", ErrUnsupportedVersion, d.Detail)
	case d.Login == LoginLoggedOut:
		return fmt.Errorf("%w: %s", ErrNeedsLogin, d.Detail)
	}
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
