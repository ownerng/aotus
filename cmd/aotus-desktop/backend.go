//go:build desktop

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"aotus/internal/client"
	"aotus/internal/datadir"
	"aotus/internal/proc"
)

// Names of the events the backend sends to the window.
const (
	EventUpdate   = "update"   // client.Update: something happened to an employee
	EventApproval = "approval" // client.Approval: an action waits for the user
	EventTerminal = "terminal" // TerminalChunk: output of a terminal
	EventDaemon   = "daemon"   // DaemonState: connected or not
	EventResync   = "resync"   // the stream fell behind: reload what is on screen
)

// DaemonState says whether the window is connected to a daemon, and which.
type DaemonState struct {
	Connected bool   `json:"connected"`
	Started   bool   `json:"started"` // this app started the daemon
	Address   string `json:"address"`
	Version   string `json:"version"`
	Error     string `json:"error"`
	// Connection is the name of the saved connection in use; "local" is this
	// computer's own daemon.
	Connection string `json:"connection"`
	Remote     bool   `json:"remote"`
	// Login and Role are who the daemon says we are (remote connections).
	Login string `json:"login"`
	Role  string `json:"role"`
}

// TerminalChunk is output of a terminal, base64 encoded because it is bytes.
type TerminalChunk struct {
	Key   string `json:"key"`
	Data  string `json:"data"`
	Ended bool   `json:"ended"`
}

// Options configures a Backend.
type Options struct {
	Layout datadir.Layout
	// ConnectionsPath is the file of saved remote connections; "" means the
	// user's config directory.
	ConnectionsPath string
	// DaemonPath is the aotusd binary; "" looks next to this program, then in
	// the PATH.
	DaemonPath string
	// StartDaemon starts the daemon so that it outlives this program. It
	// defaults to proc.StartDetached. It is only ever used for this computer's
	// daemon, never for a remote connection.
	StartDaemon func(path string, args ...string) (int, error)
	// Emit delivers an event to the window.
	Emit func(name string, data any)
	// ReadyTimeout is how long to wait for a daemon this program started.
	ReadyTimeout time.Duration
}

// Backend is everything the window needs from the daemon. It holds the client
// (and with it the token); the JavaScript only ever sees what the methods
// return. It has no GUI types, so it is tested without a window.
type Backend struct {
	o Options

	// switchMu serialises connecting, switching and closing.
	switchMu sync.Mutex

	mu    sync.Mutex
	c     *client.Client
	state DaemonState
	// target is the connection in use: client.LocalName or a saved name.
	target string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // terminal pumps

	// connCtx lives as long as one connection; follow stops with it.
	connCancel context.CancelFunc
	follows    sync.WaitGroup

	terms map[string]*client.Terminal
}

// NewBackend returns a backend that is not connected yet.
func NewBackend(o Options) *Backend {
	if o.StartDaemon == nil {
		o.StartDaemon = proc.StartDetached
	}
	if o.Emit == nil {
		o.Emit = func(string, any) {}
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 15 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Backend{o: o, ctx: ctx, cancel: cancel, terms: map[string]*client.Terminal{}}
}

func (b *Backend) connectionsPath() (string, error) {
	if b.o.ConnectionsPath != "" {
		return b.o.ConnectionsPath, nil
	}
	return client.ConnectionsPath()
}

// Connect connects to the connection the user last chose (this computer's
// daemon at first), and starts following its events. For this computer it finds
// the daemon or starts one; for a remote connection it only dials, and never
// starts anything. It is safe to call again after the connection was lost.
func (b *Backend) Connect() (DaemonState, error) {
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.mu.Lock()
	target := b.target
	connected := b.c != nil
	b.mu.Unlock()
	if connected {
		if _, err := b.c.Status(b.ctx); err == nil {
			return b.State(), nil
		}
	}
	if target == "" {
		path, err := b.connectionsPath()
		if err != nil {
			return b.fail(err)
		}
		saved, err := client.LoadConnections(path)
		if err != nil {
			return b.fail(err)
		}
		target = saved.Active
		if target == "" {
			target = client.LocalName
		}
	}
	return b.connectTo(target)
}

// State is the current state of the connection.
func (b *Backend) State() DaemonState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// connectTo drops what belongs to the old connection and opens target.
func (b *Backend) connectTo(target string) (DaemonState, error) {
	b.dropConnection()

	ctx, cancel := context.WithCancel(b.ctx)
	var c *client.Client
	var err error
	var started bool
	remote := !strings.EqualFold(target, client.LocalName)
	if remote {
		c, err = b.dialSaved(ctx, target)
	} else {
		target = client.LocalName
		c, started, err = b.connectLocal(ctx)
	}
	b.mu.Lock()
	b.target = target
	b.mu.Unlock()
	if err != nil {
		cancel()
		return b.fail(err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		cancel()
		return b.fail(err)
	}
	state := DaemonState{Connected: true, Started: started, Address: c.Address(), Version: st.Version, Connection: target, Remote: remote}
	if remote {
		if me, merr := c.Me(ctx); merr == nil {
			state.Login, state.Role = me.Login, me.Role
		}
	}
	b.mu.Lock()
	b.c, b.state, b.connCancel = c, state, cancel
	b.mu.Unlock()
	b.o.Emit(EventDaemon, state)
	b.follows.Add(1)
	go b.follow(ctx)
	return state, nil
}

// dropConnection ends the event stream and the terminals of the current
// connection, and forgets its client.
func (b *Backend) dropConnection() {
	b.mu.Lock()
	cancel := b.connCancel
	b.connCancel = nil
	terms := b.terms
	b.terms = map[string]*client.Terminal{}
	b.c = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, t := range terms {
		_ = t.Close()
	}
	b.follows.Wait()
}

// dialSaved dials a saved remote connection. Failing is an answer, not a
// reason to start anything.
func (b *Backend) dialSaved(ctx context.Context, name string) (*client.Client, error) {
	path, err := b.connectionsPath()
	if err != nil {
		return nil, err
	}
	saved, err := client.LoadConnections(path)
	if err != nil {
		return nil, err
	}
	conn, ok := saved.Get(name)
	if !ok {
		return nil, fmt.Errorf("there is no saved connection called %q", name)
	}
	return client.Dial(ctx, conn)
}

// connectLocal finds this computer's daemon, or starts it.
func (b *Backend) connectLocal(ctx context.Context) (*client.Client, bool, error) {
	c, err := client.Discover(ctx, b.o.Layout)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, client.ErrNoDaemon) {
		return nil, false, err
	}
	path, perr := b.daemonPath()
	if perr != nil {
		return nil, false, perr
	}
	if _, serr := b.o.StartDaemon(path, "--data-dir", b.o.Layout.Root); serr != nil {
		return nil, false, fmt.Errorf("starting the daemon: %w", serr)
	}
	c, err = b.waitReady(ctx)
	return c, true, err
}

func (b *Backend) fail(err error) (DaemonState, error) {
	b.mu.Lock()
	b.state.Connected, b.state.Error = false, err.Error()
	b.state.Connection, b.state.Remote = b.target, b.target != "" && !strings.EqualFold(b.target, client.LocalName)
	s := b.state
	b.mu.Unlock()
	b.o.Emit(EventDaemon, s)
	return s, err
}

func (b *Backend) waitReady(ctx context.Context) (*client.Client, error) {
	deadline := time.Now().Add(b.o.ReadyTimeout)
	for {
		c, err := client.Discover(ctx, b.o.Layout)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, client.ErrNoDaemon) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the daemon did not become ready within %s", b.o.ReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// daemonPath finds aotusd next to this program, then in the PATH.
func (b *Backend) daemonPath() (string, error) {
	if b.o.DaemonPath != "" {
		return b.o.DaemonPath, nil
	}
	name := "aotusd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	for _, p := range candidates {
		//nolint:gosec // p is built from the app's own directory and the PATH
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("cannot find %s: install it next to this program or in the PATH", name)
}

// follow forwards the daemon's events to the window until the connection ends,
// reconnecting when the stream drops. When it comes back it tells the window,
// which reloads what it shows; what is on screen is never cleared meanwhile.
func (b *Backend) follow(ctx context.Context) {
	defer b.follows.Done()
	for ctx.Err() == nil {
		c := b.client()
		if c == nil {
			return
		}
		stream, err := c.Events(ctx, "")
		if err != nil {
			b.lost(ctx, err)
			sleepCtx(ctx, time.Second)
			continue
		}
		b.up()
		for {
			m, err := stream.Next(ctx)
			if err != nil {
				_ = stream.Close()
				if client.TooSlow(err) {
					b.o.Emit(EventResync, nil)
				} else if ctx.Err() == nil {
					b.lost(ctx, err)
				}
				break
			}
			switch {
			case m.Update != nil:
				b.o.Emit(EventUpdate, m.Update)
			case m.Approval != nil:
				b.o.Emit(EventApproval, m.Approval)
			}
		}
		sleepCtx(ctx, 500*time.Millisecond)
	}
}

// up records that the stream is working. If it was down, the window is told
// and asked to reload.
func (b *Backend) up() {
	b.mu.Lock()
	was := b.state.Connected
	b.state.Connected, b.state.Error = true, ""
	s := b.state
	b.mu.Unlock()
	if !was {
		b.o.Emit(EventDaemon, s)
		b.o.Emit(EventResync, nil)
	}
}

// lost records that the daemon cannot be reached. For this computer's daemon it
// also tries to bring it back; for a remote one it only waits for the link.
func (b *Backend) lost(ctx context.Context, err error) {
	b.mu.Lock()
	was := b.state.Connected
	b.state.Connected, b.state.Error = false, err.Error()
	s := b.state
	remote := s.Remote
	b.mu.Unlock()
	if was {
		b.o.Emit(EventDaemon, s)
	}
	if remote || ctx.Err() != nil {
		return
	}
	if _, cerr := client.Discover(ctx, b.o.Layout); cerr != nil {
		// The daemon died: start it again, then pick up its new address.
		sctx, cancel := context.WithTimeout(ctx, b.o.ReadyTimeout+time.Second)
		defer cancel()
		if c, _, lerr := b.connectLocal(sctx); lerr == nil {
			b.mu.Lock()
			b.c = c
			b.mu.Unlock()
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (b *Backend) client() *client.Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.c
}

// api returns the client or the error to show when there is no daemon.
func (b *Backend) api() (*client.Client, error) {
	if c := b.client(); c != nil {
		return c, nil
	}
	return nil, client.ErrNoDaemon
}

// shutdown ends everything this program holds: event streams and terminal
// attachments. The daemon, and with it every employee, keeps running.
func (b *Backend) shutdown() {
	b.cancel()
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.dropConnection()
	b.wg.Wait()
}

// ---- terminals ----

// OpenTerminal shows the employee's own terminal (the official CLI's screen).
// The program is started if it was not running; output arrives as
// EventTerminal events under the returned key.
func (b *Backend) OpenTerminal(employeeID string, rows, cols int) (string, error) {
	c, err := b.api()
	if err != nil {
		return "", err
	}
	if err := c.StartTerminal(b.ctx, employeeID, uint16(rows), uint16(cols)); err != nil { //nolint:gosec // a terminal size is small
		return "", err
	}
	t, err := c.Attach(b.ctx, employeeID)
	if err != nil {
		return "", err
	}
	return b.track("employee:"+employeeID, t), nil
}

// OpenLogin runs the provider's login in the profile's own terminal.
func (b *Backend) OpenLogin(profileID string, rows, cols int) (string, error) {
	c, err := b.api()
	if err != nil {
		return "", err
	}
	if err := c.StartLogin(b.ctx, profileID, uint16(rows), uint16(cols)); err != nil { //nolint:gosec // a terminal size is small
		return "", err
	}
	t, err := c.AttachLogin(b.ctx, profileID)
	if err != nil {
		return "", err
	}
	return b.track("login:"+profileID, t), nil
}

func (b *Backend) track(key string, t *client.Terminal) string {
	b.mu.Lock()
	if old := b.terms[key]; old != nil {
		_ = old.Close()
	}
	b.terms[key] = t
	b.mu.Unlock()
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			p, err := t.Read(b.ctx)
			if len(p) > 0 {
				b.o.Emit(EventTerminal, TerminalChunk{Key: key, Data: base64.StdEncoding.EncodeToString(p)})
			}
			if err != nil {
				b.mu.Lock()
				if b.terms[key] == t {
					delete(b.terms, key)
				}
				b.mu.Unlock()
				if b.ctx.Err() == nil {
					b.o.Emit(EventTerminal, TerminalChunk{Key: key, Ended: true})
				}
				return
			}
		}
	}()
	return key
}

func (b *Backend) term(key string) (*client.Terminal, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.terms[key]; t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("terminal %s is not open", key)
}

// TerminalInput sends what the user typed.
func (b *Backend) TerminalInput(key, text string) error {
	t, err := b.term(key)
	if err != nil {
		return err
	}
	return t.Write(b.ctx, []byte(text))
}

// TerminalResize tells the program the size of the view.
func (b *Backend) TerminalResize(key string, rows, cols int) error {
	t, err := b.term(key)
	if err != nil {
		return err
	}
	return t.Resize(b.ctx, uint16(rows), uint16(cols)) //nolint:gosec // a terminal size is small
}

// DetachTerminal closes the view. The program keeps running in the daemon.
func (b *Backend) DetachTerminal(key string) {
	b.mu.Lock()
	t := b.terms[key]
	delete(b.terms, key)
	b.mu.Unlock()
	if t != nil {
		_ = t.Close()
	}
}
