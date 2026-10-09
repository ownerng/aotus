package netaccess

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strings"

	"aotus/internal/datadir"
)

// ErrNonLoopback means an address would expose the daemon beyond this
// computer.
var ErrNonLoopback = errors.New("netaccess: the daemon only listens on loopback")

// DefaultAddress asks the system for any free port on loopback. The real port
// is published in the discovery file.
const DefaultAddress = "127.0.0.1:0"

// CheckBind verifies that addr (host:port) is a loopback address. Wildcards
// ("0.0.0.0", "::", an empty host), hostnames other than "localhost" and any
// non-loopback IP are refused: in phase 1 nothing but this computer may reach
// the daemon. (Phase 2 adds the tailnet through a separate, authenticated
// listener.)
func CheckBind(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("netaccess: %q is not a host:port address: %w", addr, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return fmt.Errorf("%w: %q is not an IP address", ErrNonLoopback, host)
	}
	if ip.Zone() != "" || !ip.IsLoopback() {
		return fmt.Errorf("%w: %s is not a loopback address", ErrNonLoopback, ip)
	}
	return nil
}

// Listen opens the daemon's listener on a loopback address; an empty addr
// means DefaultAddress. "localhost" is pinned to 127.0.0.1 so that the name
// can never resolve to something else.
func Listen(addr string) (net.Listener, error) {
	if addr == "" {
		addr = DefaultAddress
	}
	if err := CheckBind(addr); err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(addr)
	if strings.EqualFold(host, "localhost") {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("netaccess: listening on %s: %w", addr, err)
	}
	return l, nil
}

// tokenBytes is the size of the secret: 256 bits.
const tokenBytes = 32

// LoadOrCreateToken returns the local API token, creating it on first use. The
// token is a random secret kept in an owner-only file; every request to the
// daemon must carry it, so another program running as a different user (or a
// web page, which cannot read the file) cannot talk to the daemon.
func LoadOrCreateToken(l datadir.Layout) (string, error) {
	if err := l.Ensure(); err != nil {
		return "", err
	}
	if b, err := os.ReadFile(l.Token()); err == nil {
		if tok := strings.TrimSpace(string(b)); len(tok) == tokenBytes*2 {
			if runtime.GOOS != "windows" {
				_ = os.Chmod(l.Token(), 0o600)
			}
			return tok, nil
		}
		// An unreadable or truncated token is replaced, never reused.
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("netaccess: reading the token: %w", err)
	}
	return RotateToken(l)
}

// RotateToken replaces the token with a new random one and returns it. Clients
// pick the new one up on their next connection.
func RotateToken(l datadir.Layout) (string, error) {
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netaccess: generating a token: %w", err)
	}
	tok := hex.EncodeToString(raw[:])
	// Write to a temporary file that is already 0600, then move it into place:
	// the token is never readable by others, not even for an instant.
	tmp, err := os.CreateTemp(l.Root, "token-*.tmp")
	if err != nil {
		return "", fmt.Errorf("netaccess: writing the token: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(tok + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(name, l.Token())
	}
	if werr != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("netaccess: writing the token: %w", werr)
	}
	return tok, nil
}

// TokenValid compares a presented token with the real one in constant time.
func TokenValid(presented, actual string) bool {
	return actual != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(actual)) == 1
}
