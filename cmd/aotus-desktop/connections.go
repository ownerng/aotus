//go:build desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aotus/internal/client"
)

// ConnectionInfo is a connection the window can use.
type ConnectionInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"` // "" for this computer
	Active  bool   `json:"active"`
	Remote  bool   `json:"remote"`
}

// ConnectionTest is what testing a connection found out.
type ConnectionTest struct {
	OK     bool   `json:"ok"`
	Login  string `json:"login"`
	Role   string `json:"role"`
	Device string `json:"device"`
	Error  string `json:"error"`
}

// Connections lists this computer and the saved remote daemons, and marks the
// one in use.
func (b *Backend) Connections() ([]ConnectionInfo, error) {
	path, err := b.connectionsPath()
	if err != nil {
		return nil, err
	}
	saved, err := client.LoadConnections(path)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	target := b.target
	b.mu.Unlock()
	if target == "" {
		target = saved.Active
	}
	local := target == "" || strings.EqualFold(target, client.LocalName)
	out := []ConnectionInfo{{Name: client.LocalName, Active: local}}
	for _, c := range saved.List {
		out = append(out, ConnectionInfo{Name: c.Name, Address: c.Address, Remote: true, Active: strings.EqualFold(target, c.Name)})
	}
	return out, nil
}

// AddConnection saves a remote daemon by name and address (host:port).
func (b *Backend) AddConnection(name, address string) error {
	path, err := b.connectionsPath()
	if err != nil {
		return err
	}
	saved, err := client.LoadConnections(path)
	if err != nil {
		return err
	}
	if err := saved.Add(name, strings.TrimSpace(address)); err != nil {
		return err
	}
	return saved.Save(path)
}

// RemoveConnection forgets a saved connection. If it is the one in use, the
// window goes back to this computer first.
func (b *Backend) RemoveConnection(name string) error {
	b.mu.Lock()
	using := strings.EqualFold(b.target, name)
	b.mu.Unlock()
	if using {
		if _, err := b.SwitchConnection(client.LocalName); err != nil {
			return err
		}
	}
	path, err := b.connectionsPath()
	if err != nil {
		return err
	}
	saved, err := client.LoadConnections(path)
	if err != nil {
		return err
	}
	if !saved.Remove(name) {
		return fmt.Errorf("there is no saved connection called %q", name)
	}
	return saved.Save(path)
}

// TestConnection dials a connection without switching to it, and says who we
// are there. For this computer it only looks; it never starts the daemon.
func (b *Backend) TestConnection(name string) ConnectionTest {
	ctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
	defer cancel()
	var c *client.Client
	var err error
	if strings.EqualFold(name, client.LocalName) {
		c, err = client.Discover(ctx, b.o.Layout)
		if errors.Is(err, client.ErrNoDaemon) {
			err = errors.New("this computer's daemon is not running (it starts when you switch to it)")
		}
	} else {
		c, err = b.dialSaved(ctx, name)
	}
	if err != nil {
		return ConnectionTest{Error: err.Error()}
	}
	me, err := c.Me(ctx)
	if err != nil {
		return ConnectionTest{Error: err.Error()}
	}
	return ConnectionTest{OK: true, Login: me.Login, Role: me.Role, Device: me.Device}
}

// SwitchConnection makes the window work against another daemon. The event
// stream, the open terminals and the client of the old connection are dropped
// first, and the window is told to reload everything. If the new one cannot be
// reached the old one is restored, so the window is never left with nothing.
func (b *Backend) SwitchConnection(name string) (DaemonState, error) {
	b.switchMu.Lock()
	defer b.switchMu.Unlock()
	b.mu.Lock()
	previous := b.target
	b.mu.Unlock()

	state, err := b.connectTo(name)
	if err != nil {
		if previous != "" {
			_, _ = b.connectTo(previous) // best effort: back to where we were
		}
		return state, err
	}
	path, perr := b.connectionsPath()
	if perr == nil {
		if saved, lerr := client.LoadConnections(path); lerr == nil {
			saved.Active = state.Connection
			if state.Connection == client.LocalName {
				saved.Active = ""
			}
			_ = saved.Save(path)
		}
	}
	b.o.Emit(EventResync, nil)
	return state, nil
}
