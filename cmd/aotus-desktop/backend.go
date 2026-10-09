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

// DaemonState says whether the window is connected to the daemon.
type DaemonState struct {
	Connected bool   `json:"connected"`
	Started   bool   `json:"started"` // this app started the daemon
	Address   string `json:"address"`
	Version   string `json:"version"`
	Error     string `json:"error"`
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
	// DaemonPath is the aotusd binary; "" looks next to this program, then in
	// the PATH.
	DaemonPath string
	// StartDaemon starts the daemon so that it outlives this program. It
	// defaults to proc.StartDetached.
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

	mu    sync.Mutex
	c     *client.Client
	state DaemonState

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

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

// Connect finds the running daemon, or starts one, and starts following its
// events. It is safe to call again after the connection was lost.
func (b *Backend) Connect() (DaemonState, error) {
	return b.connect(b.ctx)
}

func (b *Backend) connect(ctx context.Context) (DaemonState, error) {
	b.mu.Lock()
	if b.c != nil {
		if _, err := b.c.Status(ctx); err == nil {
			s := b.state
			b.mu.Unlock()
			return s, nil
		}
		b.c = nil
	}
	b.mu.Unlock()

	started := false
	c, err := client.Discover(ctx, b.o.Layout)
	if errors.Is(err, client.ErrNoDaemon) {
		path, perr := b.daemonPath()
		if perr != nil {
			return b.fail(perr)
		}
		if _, serr := b.o.StartDaemon(path, "--data-dir", b.o.Layout.Root); serr != nil {
			return b.fail(fmt.Errorf("starting the daemon: %w", serr))
		}
		started = true
		c, err = b.waitReady(ctx)
	}
	if err != nil {
		return b.fail(err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		return b.fail(err)
	}

	b.mu.Lock()
	first := b.c == nil && b.state.Address == ""
	b.c = c
	b.state = DaemonState{Connected: true, Started: started || b.state.Started, Address: c.Address(), Version: st.Version}
	s := b.state
	b.mu.Unlock()
	b.o.Emit(EventDaemon, s)
	if first {
		b.wg.Add(1)
		go b.follow()
	}
	return s, nil
}

func (b *Backend) fail(err error) (DaemonState, error) {
	s := DaemonState{Error: err.Error()}
	b.mu.Lock()
	b.state.Connected, b.state.Error = false, s.Error
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

// follow forwards the daemon's events to the window, reconnecting when the
// stream drops.
func (b *Backend) follow() {
	defer b.wg.Done()
	for b.ctx.Err() == nil {
		c := b.client()
		if c == nil {
			b.sleep(time.Second)
			continue
		}
		stream, err := c.Events(b.ctx, "")
		if err != nil {
			b.lost(err)
			b.sleep(time.Second)
			continue
		}
		for {
			m, err := stream.Next(b.ctx)
			if err != nil {
				_ = stream.Close()
				if client.TooSlow(err) {
					b.o.Emit(EventResync, nil)
				} else if b.ctx.Err() == nil {
					b.lost(err)
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
		b.sleep(500 * time.Millisecond)
	}
}

// lost records that the daemon cannot be reached and tries to bring it back.
func (b *Backend) lost(err error) {
	b.mu.Lock()
	was := b.state.Connected
	b.state.Connected, b.state.Error = false, err.Error()
	s := b.state
	b.mu.Unlock()
	if was {
		b.o.Emit(EventDaemon, s)
	}
	if _, cerr := client.Discover(b.ctx, b.o.Layout); cerr != nil {
		ctx, cancel := context.WithTimeout(b.ctx, b.o.ReadyTimeout+time.Second)
		defer cancel()
		b.mu.Lock()
		b.c = nil
		b.mu.Unlock()
		_, _ = b.connect(ctx) // starts the daemon again if it died
		return
	}
	b.mu.Lock()
	b.state.Connected, b.state.Error = true, ""
	s = b.state
	b.mu.Unlock()
	b.o.Emit(EventDaemon, s)
}

func (b *Backend) sleep(d time.Duration) {
	select {
	case <-b.ctx.Done():
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
	b.mu.Lock()
	terms := b.terms
	b.terms = map[string]*client.Terminal{}
	b.mu.Unlock()
	for _, t := range terms {
		_ = t.Close()
	}
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
