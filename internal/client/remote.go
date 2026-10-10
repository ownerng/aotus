package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aotus/internal/datadir"
)

// Remote connections. The daemon of another computer (a VPS) is reached over
// the tailnet by name. Nothing secret is involved: the daemon learns who we are
// from Tailscale, so a connection is only a name and an address, and the
// connections file holds nothing else.

// Why a remote call failed, in terms the user can act on. Use errors.Is.
var (
	// ErrNotAllowed: the daemon knows who we are and has not allowed us.
	ErrNotAllowed = errors.New("this login is not on the daemon's allow-list")
	// ErrTailnetDown: the address cannot be found or reached from here, which
	// usually means Tailscale is not running on this computer.
	ErrTailnetDown = errors.New("cannot reach the tailnet from this computer")
	// ErrUnreachable: the address was reached, or should have been, and the
	// daemon did not answer.
	ErrUnreachable = errors.New("the daemon did not answer")
)

// Connection is a saved remote daemon.
type Connection struct {
	Name    string `json:"name"`
	Address string `json:"address"` // host:port, the daemon's tailnet name and port
}

// LocalName is the name that always means this computer's own daemon.
const LocalName = "local"

// Connections is the file of saved connections.
type Connections struct {
	// Active is the connection the desktop app opens with; "" or "local" is
	// this computer.
	Active string       `json:"active,omitempty"`
	List   []Connection `json:"connections"`
}

// ConnectionsPath is where the file lives: the user's config directory, apart
// from the daemon's data.
func ConnectionsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aotus", "connections.json"), nil
}

// LoadConnections reads the file; a missing one is an empty list.
func LoadConnections(path string) (Connections, error) {
	var c Connections
	b, err := os.ReadFile(path) //nolint:gosec // the path is the user's own connections file
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Connections{}, fmt.Errorf("client: %s is damaged: %w", path, err)
	}
	return c, nil
}

// Save writes the file with owner-only permissions.
func (c Connections) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Slice(c.List, func(i, j int) bool { return c.List[i].Name < c.List[j].Name })
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get finds a connection by name.
func (c Connections) Get(name string) (Connection, bool) {
	for _, x := range c.List {
		if strings.EqualFold(x.Name, name) {
			return x, true
		}
	}
	return Connection{}, false
}

// ValidateAddress accepts only host:port. A URL, credentials, a path or a
// query are refused, so a secret can never end up in the file by mistake.
func ValidateAddress(addr string) error {
	if strings.ContainsAny(addr, "@/?#\\ ") || strings.Contains(addr, "://") {
		return fmt.Errorf("client: %q is not a host:port address (no scheme, credentials or path)", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("client: %q is not a host:port address, for example vps.tail1234.ts.net:7843", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("client: %q has no valid port", addr)
	}
	return nil
}

// Add saves a connection, replacing one of the same name.
func (c *Connections) Add(name, addr string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, LocalName) || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("client: %q cannot be used as a connection name", name)
	}
	if err := ValidateAddress(addr); err != nil {
		return err
	}
	for i, x := range c.List {
		if strings.EqualFold(x.Name, name) {
			c.List[i] = Connection{Name: name, Address: addr}
			return nil
		}
	}
	c.List = append(c.List, Connection{Name: name, Address: addr})
	return nil
}

// Remove forgets a connection; if it was the active one, the active one
// becomes this computer.
func (c *Connections) Remove(name string) bool {
	for i, x := range c.List {
		if strings.EqualFold(x.Name, name) {
			c.List = append(c.List[:i], c.List[i+1:]...)
			if strings.EqualFold(c.Active, name) {
				c.Active = ""
			}
			return true
		}
	}
	return false
}

// Dial connects to a remote daemon and checks that it serves us. There is no
// token: the daemon asks Tailscale who we are.
func Dial(ctx context.Context, conn Connection) (*Client, error) {
	if err := ValidateAddress(conn.Address); err != nil {
		return nil, err
	}
	c := &Client{
		base:   "http://" + conn.Address + "/api/v1",
		http:   &http.Client{Timeout: 60 * time.Second},
		remote: conn.Name,
	}
	if _, err := c.Status(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Connect opens the connection called name: this computer's daemon for ""
// and "local", else a saved remote one read from connectionsPath.
func Connect(ctx context.Context, l datadir.Layout, connectionsPath, name string) (*Client, error) {
	if name == "" || strings.EqualFold(name, LocalName) {
		return Discover(ctx, l)
	}
	saved, err := LoadConnections(connectionsPath)
	if err != nil {
		return nil, err
	}
	conn, ok := saved.Get(name)
	if !ok {
		return nil, fmt.Errorf("client: there is no saved connection called %q (see `aotus connections`)", name)
	}
	return Dial(ctx, conn)
}

// Remote reports the name of the saved connection this client uses, or "" for
// the local daemon.
func (c *Client) Remote() string { return c.remote }

// explain turns a failure of the network or of the daemon into one of the
// errors above, with what to do about it. Other errors pass through.
func (c *Client) explain(err error) error {
	if err == nil || c.remote == "" {
		return err
	}
	var ae *APIError
	if errors.As(err, &ae) {
		if ae.Code == "not_allowed" {
			return fmt.Errorf("%w: ask the owner of %s to run `aotus access allow YOUR-LOGIN`", ErrNotAllowed, c.remote)
		}
		return err
	}
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH), mentions(err, "network is unreachable", "no route to host", "host is unreachable"):
		return fmt.Errorf("%w: is Tailscale running here, and is the name of %s right? (%w)", ErrTailnetDown, c.remote, err)
	case errors.Is(err, syscall.ECONNREFUSED), mentions(err, "actively refused", "connection refused"):
		return fmt.Errorf("%w: nothing listens at that address; is aotusd running with --tailnet on the server? (%w)", ErrUnreachable, err)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET), mentions(err, "connection reset", "forcibly closed", "connection was aborted"):
		return fmt.Errorf("%w: the daemon closed the connection. It does that to a device it cannot tie to a person (a tagged device) or when its own Tailscale cannot answer (%w)", ErrUnreachable, err)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("%w: it did not answer in time; is the server up and in your tailnet? (%w)", ErrUnreachable, err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return err
}

// ---- who am I, and the allow-list ----

// Me is who the daemon thinks the caller is.
type Me struct {
	Method string `json:"method"`
	Login  string `json:"login"`
	Device string `json:"device"`
	Role   string `json:"role"` // owner or guest
}

// AccessEntry is a person on the allow-list.
type AccessEntry struct {
	Login    string   `json:"login"`
	AddedAt  string   `json:"added_at"`
	AddedBy  string   `json:"added_by"`
	Profiles []string `json:"profiles"`
}

// Access is the allow-list and the notice the owner acknowledges to add
// someone.
type Access struct {
	Owner         string        `json:"owner"`
	Entries       []AccessEntry `json:"entries"`
	SharingNotice string        `json:"sharing_notice"`
}

// Me asks who the daemon thinks we are.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	return m, c.do(ctx, http.MethodGet, "/me", nil, &m)
}

// Access reads the allow-list (owner only).
func (c *Client) Access(ctx context.Context) (Access, error) {
	var a Access
	return a, c.do(ctx, http.MethodGet, "/access", nil, &a)
}

// AllowLogin adds a person. acknowledged must be true after the owner has read
// the sharing notice.
func (c *Client) AllowLogin(ctx context.Context, login string, acknowledged bool) error {
	return c.do(ctx, http.MethodPost, "/access", map[string]any{"login": login, "acknowledged": acknowledged}, nil)
}

// DenyLogin removes a person and everything shared with them.
func (c *Client) DenyLogin(ctx context.Context, login string) error {
	return c.do(ctx, http.MethodDelete, "/access/"+url.PathEscape(login), nil, nil)
}

// ShareProfile lets a listed person use a profile.
func (c *Client) ShareProfile(ctx context.Context, profileID, login string) error {
	return c.do(ctx, http.MethodPost, "/profiles/"+url.PathEscape(profileID)+"/shares", map[string]string{"login": login}, nil)
}

// UnshareProfile withdraws it.
func (c *Client) UnshareProfile(ctx context.Context, profileID, login string) error {
	return c.do(ctx, http.MethodDelete, "/profiles/"+url.PathEscape(profileID)+"/shares/"+url.PathEscape(login), nil, nil)
}

// ---- following events across dropped links ----

// Follow streams the daemon's events to handle until ctx ends, reconnecting
// with back-off when the link drops or the daemon restarts. state is told when
// the stream is up (true) or down (false); it may be nil. After a reconnect the
// first message is a "hello" again, and anything that happened meanwhile is not
// replayed: callers reload what they show (they are told through state).
func (c *Client) Follow(ctx context.Context, employeeID string, handle func(Message), state func(connected bool)) {
	delay := 200 * time.Millisecond
	for ctx.Err() == nil {
		stream, err := c.Events(ctx, employeeID)
		if err == nil {
			delay = 200 * time.Millisecond
			if state != nil {
				state(true)
			}
			for {
				m, nerr := stream.Next(ctx)
				if nerr != nil {
					_ = stream.Close()
					break
				}
				handle(m)
			}
			if state != nil && ctx.Err() == nil {
				state(false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}

// mentions reports whether the error text contains one of the phrases. Windows
// reports the same network failures with its own error numbers that errors.Is
// does not map to the syscall constants, so the text is the common ground.
func mentions(err error, phrases ...string) bool {
	text := strings.ToLower(err.Error())
	for _, p := range phrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
