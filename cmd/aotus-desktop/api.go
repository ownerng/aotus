//go:build desktop

package main

import (
	"context"
	"time"

	"aotus/internal/client"
)

// The methods below are what the window calls. Each one is a thin pass to the
// daemon API through internal/client; the window never sees the token or the
// address.

// callTimeout bounds one request; streaming goes through events, not calls.
const callTimeout = 30 * time.Second

func call[T any](b *Backend, f func(ctx context.Context, c *client.Client) (T, error)) (T, error) {
	var zero T
	c, err := b.api()
	if err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(b.ctx, callTimeout)
	defer cancel()
	return f(ctx, c)
}

func do(b *Backend, f func(ctx context.Context, c *client.Client) error) error {
	_, err := call(b, func(ctx context.Context, c *client.Client) (struct{}, error) { return struct{}{}, f(ctx, c) })
	return err
}

// Profiles lists the linked subscriptions.
func (b *Backend) Profiles() ([]client.Profile, error) {
	return call(b, func(ctx context.Context, c *client.Client) ([]client.Profile, error) { return c.Profiles(ctx) })
}

// CreateProfile links a subscription (or an API key profile).
func (b *Backend) CreateProfile(in client.NewProfile) (client.Profile, error) {
	return call(b, func(ctx context.Context, c *client.Client) (client.Profile, error) { return c.CreateProfile(ctx, in) })
}

// DeleteProfile unlinks a subscription.
func (b *Backend) DeleteProfile(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.DeleteProfile(ctx, id) })
}

// Detect asks what the CLI of a profile reports: installed, version, login.
func (b *Backend) Detect(id string) (client.Detection, error) {
	return call(b, func(ctx context.Context, c *client.Client) (client.Detection, error) { return c.Detect(ctx, id) })
}

// AcceptNotice records that the user accepted a notice (for example claude-headless).
func (b *Backend) AcceptNotice(id, notice string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.AcceptNotice(ctx, id, notice) })
}

// SetAPIKey stores an API key in the OS credential store.
func (b *Backend) SetAPIKey(id, key string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.SetAPIKey(ctx, id, key) })
}

// Employees lists the employees.
func (b *Backend) Employees() ([]client.Employee, error) {
	return call(b, func(ctx context.Context, c *client.Client) ([]client.Employee, error) { return c.Employees(ctx) })
}

// CreateEmployee hires an employee.
func (b *Backend) CreateEmployee(in client.NewEmployee) (client.Employee, error) {
	return call(b, func(ctx context.Context, c *client.Client) (client.Employee, error) { return c.CreateEmployee(ctx, in) })
}

// DeleteEmployee removes an employee.
func (b *Backend) DeleteEmployee(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.DeleteEmployee(ctx, id) })
}

// Pause stops an employee's session without deleting anything.
func (b *Backend) Pause(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.Pause(ctx, id) })
}

// Resume brings a paused employee back.
func (b *Backend) Resume(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.Resume(ctx, id) })
}

// Send gives the employee a prompt and returns the ID of the turn. The answer
// streams as update events.
func (b *Backend) Send(id, prompt string) (string, error) {
	return call(b, func(ctx context.Context, c *client.Client) (string, error) { return c.Send(ctx, id, prompt) })
}

// Cancel ends the running turn.
func (b *Backend) Cancel(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.Cancel(ctx, id) })
}

// History returns a page of past turns, newest first; before is the started_at of the
// oldest turn already shown ("" for the first page).
func (b *Backend) History(id string, limit int, before string) ([]client.Turn, error) {
	return call(b, func(ctx context.Context, c *client.Client) ([]client.Turn, error) {
		return c.History(ctx, id, limit, before)
	})
}

// TurnEvents returns the recorded events of one turn, loaded when it is opened.
func (b *Backend) TurnEvents(turnID string) ([]client.Event, error) {
	return call(b, func(ctx context.Context, c *client.Client) ([]client.Event, error) { return c.TurnEvents(ctx, turnID) })
}

// StopTerminal ends the employee's terminal program on purpose.
func (b *Backend) StopTerminal(id string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.StopTerminal(ctx, id) })
}

// Approvals lists the actions waiting for the user.
func (b *Backend) Approvals() ([]client.Approval, error) {
	return call(b, func(ctx context.Context, c *client.Client) ([]client.Approval, error) { return c.Approvals(ctx) })
}

// Answer allows or denies an action; remember is none, exact or kind.
func (b *Backend) Answer(requestID string, allow bool, remember string) error {
	return do(b, func(ctx context.Context, c *client.Client) error { return c.Answer(ctx, requestID, allow, remember) })
}
