package netaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
)

// Method says how a caller proved who it is.
type Method string

const (
	// MethodToken is the local bearer token, valid only on loopback.
	MethodToken Method = "token"
	// MethodTailnet is the identity Tailscale vouches for, valid only on the
	// tailnet listener.
	MethodTailnet Method = "tailnet"
)

// Caller is who is on the other end of a request.
type Caller struct {
	Method Method
	// Login is the person: the Tailscale login name (empty for the local token,
	// which belongs to whoever can read the data directory).
	Login string
	// Device is the machine the request comes from, as the tailnet names it.
	Device string
	// Tags are the tailnet tags of the device. A device that has tags and no
	// person is refused by the identifier, never reaches here.
	Tags []string
}

// String is for logs and the audit trail: never contains a secret.
func (c Caller) String() string {
	switch {
	case c.Login != "" && c.Device != "":
		return c.Login + " (" + c.Device + ")"
	case c.Login != "":
		return c.Login
	case c.Method == MethodToken:
		return "local"
	}
	return string(c.Method)
}

type callerKey struct{}

// WithCaller returns a context that carries the caller.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller a request context carries.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// ErrUnidentified means the other end could not be tied to a person: the
// connection is refused.
var ErrUnidentified = errors.New("netaccess: the caller could not be identified")

// Identifier tells who is behind a remote address. The real one asks the
// Tailscale running on this machine; tests use StaticIdentifier. It must be
// safe for concurrent use.
type Identifier interface {
	// Identify returns the caller for remoteAddr ("ip:port"), or an error that
	// wraps ErrUnidentified.
	Identify(ctx context.Context, remoteAddr string) (Caller, error)
}

// StaticIdentifier is an Identifier with a fixed table, keyed by the remote IP
// (without the port). It is for tests and tools.
type StaticIdentifier map[string]Caller

// Identify implements Identifier.
func (s StaticIdentifier) Identify(_ context.Context, remoteAddr string) (Caller, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if c, ok := s[host]; ok {
		return c, nil
	}
	return Caller{}, fmt.Errorf("%w: %s is not known", ErrUnidentified, host)
}

// The two address ranges Tailscale hands out: IPv4 100.64.0.0/10 and the
// IPv6 unique-local range fd7a:115c:a1e0::/48.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// ErrNotTailnet means an address is not one a tailnet gives out, so the daemon
// would be exposing itself somewhere else.
var ErrNotTailnet = errors.New("netaccess: not a tailnet address")

// CheckTailnetAddr accepts only an address from Tailscale's own ranges. A
// wildcard, a loopback, a LAN or a public address is refused: the tailnet
// listener can never be pointed at anything else.
func CheckTailnetAddr(ip netip.Addr) error {
	ip = ip.Unmap()
	if ip.Zone() == "" && (tailnetV4.Contains(ip) || tailnetV6.Contains(ip)) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotTailnet, ip)
}

// ListenTailnet opens the tailnet listener on the machine's tailnet address.
// Every accepted connection is identified before the HTTP server sees it; one
// that cannot be identified is closed, and the reason is logged. Use
// ConnContext as http.Server.ConnContext so handlers can read the Caller.
func ListenTailnet(ip netip.Addr, port string, id Identifier, log *slog.Logger) (net.Listener, error) {
	if err := CheckTailnetAddr(ip); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, errors.New("netaccess: a tailnet listener needs an identifier")
	}
	addr := net.JoinHostPort(ip.Unmap().String(), port)
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("netaccess: listening on %s: %w", addr, err)
	}
	return IdentifyListener(l, id, log), nil
}

// IdentifyListener wraps a listener so that Accept only returns connections
// whose peer id could tie to a person. It opens nothing itself; ListenTailnet
// is the only place in the product that picks the address. Tests wrap a
// listener of their own.
func IdentifyListener(l net.Listener, id Identifier, log *slog.Logger) net.Listener {
	if log == nil {
		log = slog.Default()
	}
	return &identifiedListener{Listener: l, id: id, log: log}
}

// listenIdentified is for the tests of this package: a loopback listener with
// identification.
func listenIdentified(addr string, id Identifier, log *slog.Logger) (net.Listener, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	return IdentifyListener(l, id, log), nil
}

type identifiedListener struct {
	net.Listener
	id  Identifier
	log *slog.Logger
}

// callerConn is a connection whose peer has been identified.
type callerConn struct {
	net.Conn
	caller Caller
}

// Accept returns only identified connections.
func (l *identifiedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		who, err := l.id.Identify(context.Background(), c.RemoteAddr().String())
		if err != nil {
			l.log.Warn("tailnet connection refused", "remote", c.RemoteAddr().String(), "reason", err.Error())
			_ = c.Close()
			continue
		}
		who.Method = MethodTailnet
		who.Login = strings.TrimSpace(who.Login)
		if who.Login == "" {
			l.log.Warn("tailnet connection refused", "remote", c.RemoteAddr().String(), "reason", "no person behind the device")
			_ = c.Close()
			continue
		}
		return &callerConn{Conn: c, caller: who}, nil
	}
}

// ConnContext is for http.Server.ConnContext: it puts the caller of an
// identified connection in the context of its requests. Connections that did
// not come through ListenTailnet carry no caller.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if cc, ok := c.(*callerConn); ok {
		return WithCaller(ctx, cc.caller)
	}
	return ctx
}

// Kind is the kind of listener the configuration asks for. There are exactly
// two; no value means "everywhere".
type Kind string

const (
	KindLoopback Kind = "loopback"
	KindTailnet  Kind = "tailnet"
)

// ParseKind reads a configuration value; empty means loopback, the default.
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "loopback":
		return KindLoopback, nil
	case "tailnet":
		return KindTailnet, nil
	}
	return "", fmt.Errorf("netaccess: unknown listener kind %q (use loopback or tailnet)", s)
}
