package netaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The Tailscale LocalAPI is an HTTP API the local Tailscale daemon serves on a
// Unix socket. We use two calls, both documented by Tailscale as stable
// (status and whois), with the standard library only (ADR 0014).

// DefaultSocket is where Tailscale's LocalAPI socket is on this system.
func DefaultSocket() string {
	if runtime.GOOS == "darwin" {
		return "/var/run/tailscaled.socket"
	}
	return "/var/run/tailscale/tailscaled.sock"
}

// maxLocalAPIBody bounds what we read from the LocalAPI.
const maxLocalAPIBody = 4 << 20

// ErrTailscaleUnavailable means the local Tailscale could not be asked.
var ErrTailscaleUnavailable = errors.New("netaccess: cannot reach the local Tailscale")

// LocalAPI talks to the Tailscale running on this machine. It is an
// Identifier.
type LocalAPI struct {
	hc *http.Client
}

// NewLocalAPI returns a client for the LocalAPI socket at path.
func NewLocalAPI(socket string) *LocalAPI {
	return &LocalAPI{hc: &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

// get calls the LocalAPI and decodes the JSON answer. The host name is fixed
// by the protocol: Tailscale refuses any other.
func (l *LocalAPI) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscaled.sock"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := l.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrTailscaleUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLocalAPIBody))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("%w: %w", ErrTailscaleUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("the local Tailscale answered %s to %s: %s", resp.Status, path, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("the local Tailscale's answer to %s is not what we expect: %w", path, err)
	}
	return resp.StatusCode, nil
}

// TailnetStatus is what the daemon needs to know about its own node.
type TailnetStatus struct {
	// Running is true when Tailscale is connected.
	Running bool
	State   string // Tailscale's BackendState: Running, Stopped, NeedsLogin...
	// Names are the names the daemon answers to: MagicDNS name (without the
	// trailing dot), short host name, and the tailnet addresses.
	Names []string
	// Addrs are the node's tailnet addresses, IPv4 first.
	Addrs []netip.Addr
	// OwnerLogin is the person the node belongs to; empty for a tagged node.
	OwnerLogin string
	Tagged     bool
}

type statusJSON struct {
	BackendState string
	TailscaleIPs []netip.Addr
	Self         *struct {
		HostName     string
		DNSName      string
		UserID       int64
		Tags         []string
		TailscaleIPs []netip.Addr
	}
	User map[string]struct{ LoginName string }
}

// Status asks Tailscale about this machine's node.
func (l *LocalAPI) Status(ctx context.Context) (TailnetStatus, error) {
	var st statusJSON
	if _, err := l.get(ctx, "/localapi/v0/status?peers=false", &st); err != nil {
		return TailnetStatus{}, err
	}
	out := TailnetStatus{State: st.BackendState, Running: st.BackendState == "Running"}
	addrs := st.TailscaleIPs
	if st.Self != nil {
		if len(addrs) == 0 {
			addrs = st.Self.TailscaleIPs
		}
		out.Tagged = len(st.Self.Tags) > 0
		if !out.Tagged {
			out.OwnerLogin = strings.TrimSpace(st.User[strconv.FormatInt(st.Self.UserID, 10)].LoginName)
		}
		if n := strings.TrimSuffix(st.Self.DNSName, "."); n != "" {
			out.Names = append(out.Names, n)
		}
		if st.Self.HostName != "" {
			out.Names = append(out.Names, st.Self.HostName)
		}
	}
	// IPv4 first: it is what clients dial.
	for _, a := range addrs {
		if a.Unmap().Is4() {
			out.Addrs = append(out.Addrs, a.Unmap())
		}
	}
	for _, a := range addrs {
		if !a.Unmap().Is4() {
			out.Addrs = append(out.Addrs, a)
		}
	}
	for _, a := range out.Addrs {
		out.Names = append(out.Names, a.String())
	}
	return out, nil
}

type whoisJSON struct {
	Node *struct {
		ComputedName string
		Name         string
		Tags         []string
	}
	UserProfile *struct{ LoginName string }
}

// Identify implements Identifier with Tailscale's whois. A peer Tailscale does
// not know, a device owned by a tag, or one with no login is unidentified.
func (l *LocalAPI) Identify(ctx context.Context, remoteAddr string) (Caller, error) {
	var w whoisJSON
	code, err := l.get(ctx, "/localapi/v0/whois?addr="+url.QueryEscape(remoteAddr), &w)
	if err != nil {
		if code == http.StatusNotFound {
			return Caller{}, fmt.Errorf("%w: Tailscale does not know %s", ErrUnidentified, remoteAddr)
		}
		return Caller{}, fmt.Errorf("%w: %w", ErrUnidentified, err)
	}
	if w.Node == nil || w.UserProfile == nil {
		return Caller{}, fmt.Errorf("%w: Tailscale gave no device or person for %s", ErrUnidentified, remoteAddr)
	}
	if len(w.Node.Tags) > 0 {
		return Caller{}, fmt.Errorf("%w: %s is a tagged device (%s), not a person", ErrUnidentified, w.Node.ComputedName, strings.Join(w.Node.Tags, ","))
	}
	login := strings.TrimSpace(w.UserProfile.LoginName)
	if login == "" || login == "tagged-devices" {
		return Caller{}, fmt.Errorf("%w: no person behind %s", ErrUnidentified, w.Node.ComputedName)
	}
	device := w.Node.ComputedName
	if device == "" {
		device = strings.TrimSuffix(w.Node.Name, ".")
	}
	return Caller{Method: MethodTailnet, Login: login, Device: device}, nil
}
