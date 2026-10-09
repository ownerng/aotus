// Package client is the Go client of the daemon API (docs/API.md). The desktop
// app and the aotus command are built on it; it speaks only HTTP and WebSocket
// and knows nothing about the daemon's internals.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"aotus/internal/datadir"
	"aotus/internal/lifecycle"
)

// ErrNoDaemon means no daemon answers: it is not running, or its address in
// the discovery file is stale.
var ErrNoDaemon = errors.New("no Aotus daemon is running")

// APIError is an error answered by the daemon.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (%s)", e.Message, e.Code) }

// HasCode reports whether err is an API error with this code, for example
// "busy", "name_taken" or "notice_required".
func HasCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// Client talks to one daemon.
type Client struct {
	base  string // http://127.0.0.1:port/api/v1
	token string
	http  *http.Client
}

// New returns a client for a daemon at address (host:port) with a token.
func New(address, token string) *Client {
	return &Client{base: "http://" + address + "/api/v1", token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

// Discover finds the running daemon of a data directory through its discovery
// file and local token, and checks that it answers.
func Discover(ctx context.Context, l datadir.Layout) (*Client, error) {
	d, err := lifecycle.ReadDiscovery(l)
	if errors.Is(err, lifecycle.ErrNoDaemon) {
		return nil, ErrNoDaemon
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(l.Token())
	if err != nil {
		return nil, fmt.Errorf("client: reading the token: %w", err)
	}
	c := New(d.Address, strings.TrimSpace(string(b)))
	if _, err := c.Status(ctx); err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			return nil, err // it answers but refuses us
		}
		return nil, fmt.Errorf("%w (the discovery file points at %s, which does not answer)", ErrNoDaemon, d.Address)
	}
	return c, nil
}

// Address is host:port of the daemon.
func (c *Client) Address() string {
	return strings.TrimSuffix(strings.TrimPrefix(c.base, "http://"), "/api/v1")
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct{ Code, Message string }
		}
		ae := &APIError{Status: resp.StatusCode, Code: "http_" + fmt.Sprint(resp.StatusCode), Message: strings.TrimSpace(string(data))}
		if json.Unmarshal(data, &e) == nil && e.Error.Code != "" {
			ae.Code, ae.Message = e.Error.Code, e.Error.Message
		}
		return ae
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ---- types (see docs/API.md) ----

// Status is the daemon's answer to Status.
type Status struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	Employees int    `json:"employees"`
}

// Profile is a subscription profile.
type Profile struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Binary          string            `json:"binary"`
	Mode            string            `json:"mode"`
	Model           string            `json:"model"`
	BaseURL         string            `json:"base_url,omitempty"`
	AcceptedNotices []string          `json:"accepted_notices"`
	ExtraEnv        map[string]string `json:"extra_env,omitempty"`
}

// NewProfile is the input of CreateProfile.
type NewProfile struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Binary   string            `json:"binary,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Model    string            `json:"model,omitempty"`
	BaseURL  string            `json:"base_url,omitempty"`
	ExtraEnv map[string]string `json:"extra_env,omitempty"`
}

// Detection is what the daemon found out about a profile's CLI.
type Detection struct {
	Installed bool     `json:"installed"`
	Version   string   `json:"version"`
	VersionOK bool     `json:"version_ok"`
	Modes     []string `json:"modes"`
	Login     string   `json:"login"`
	Detail    string   `json:"detail"`
}

// Employee is an agent and what it is doing.
type Employee struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	SystemPrompt    string   `json:"system_prompt"`
	ProfileID       string   `json:"profile_id"`
	State           string   `json:"state"`
	PermissionMode  string   `json:"permission_mode"`
	AllowedTools    []string `json:"allowed_tools"`
	CreatedAt       string   `json:"created_at"`
	Mode            string   `json:"mode"`
	Working         bool     `json:"working"`
	TurnID          string   `json:"turn_id"`
	TerminalRunning bool     `json:"terminal_running"`
}

// NewEmployee is the input of CreateEmployee.
type NewEmployee struct {
	Name           string   `json:"name"`
	Role           string   `json:"role,omitempty"`
	SystemPrompt   string   `json:"system_prompt,omitempty"`
	ProfileID      string   `json:"profile_id"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
}

// Turn is a request to an employee and how it ended.
type Turn struct {
	ID           string  `json:"id"`
	EmployeeID   string  `json:"employee_id"`
	Prompt       string  `json:"prompt"`
	State        string  `json:"state"`
	Error        string  `json:"error"`
	StartedAt    string  `json:"started_at"`
	EndedAt      string  `json:"ended_at"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

// Event is a normalized provider event.
type Event struct {
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	SessionID string          `json:"session_id"`
	Text      string          `json:"text"`
	Code      string          `json:"code"`
	ToolID    string          `json:"tool_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolOut   string          `json:"tool_output"`
	ToolError bool            `json:"tool_error"`
	Reason    string          `json:"reason"`
	CostUSD   float64         `json:"cost_usd"`
	InTokens  int             `json:"input_tokens"`
	OutTokens int             `json:"output_tokens"`
}

// Update is something that happened to an employee.
type Update struct {
	Seq        uint64 `json:"seq"`
	Kind       string `json:"kind"`
	EmployeeID string `json:"employee_id"`
	TurnID     string `json:"turn_id"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	Event      *Event `json:"event"`
}

// Approval is an action that waits for the user.
type Approval struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Kind       string `json:"kind"`
	Target     string `json:"target"`
	At         string `json:"at"`
}

// Grant is a remembered permission decision.
type Grant struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Allow  bool   `json:"allow"`
}

// Fact is something an employee remembers.
type Fact struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// Hit is a memory search result.
type Hit struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Snippet string `json:"snippet"`
}

// ---- calls ----

// Status asks the daemon how it is.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	return s, c.do(ctx, "GET", "/status", nil, &s)
}

// Profiles lists the subscription profiles.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var out []Profile
	return out, c.do(ctx, "GET", "/profiles", nil, &out)
}

// CreateProfile makes a new profile.
func (c *Client) CreateProfile(ctx context.Context, in NewProfile) (Profile, error) {
	var out Profile
	return out, c.do(ctx, "POST", "/profiles", in, &out)
}

// DeleteProfile removes a profile that no employee uses.
func (c *Client) DeleteProfile(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/profiles/"+url.PathEscape(id), nil, nil)
}

// Detect inspects the CLI of a profile.
func (c *Client) Detect(ctx context.Context, id string) (Detection, error) {
	var out Detection
	return out, c.do(ctx, "GET", "/profiles/"+url.PathEscape(id)+"/detect", nil, &out)
}

// AcceptNotice records that the user accepted a notice for a profile.
func (c *Client) AcceptNotice(ctx context.Context, id, notice string) error {
	return c.do(ctx, "POST", "/profiles/"+url.PathEscape(id)+"/notices", map[string]string{"notice": notice}, nil)
}

// SetAPIKey stores a profile's API key in the daemon's credential store.
func (c *Client) SetAPIKey(ctx context.Context, id, key string) error {
	return c.do(ctx, "PUT", "/profiles/"+url.PathEscape(id)+"/api-key", map[string]string{"key": key}, nil)
}

// RemoveAPIKey deletes a profile's API key.
func (c *Client) RemoveAPIKey(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/profiles/"+url.PathEscape(id)+"/api-key", nil, nil)
}

// StartLogin starts the CLI's own login flow for a profile in a terminal.
func (c *Client) StartLogin(ctx context.Context, id string, rows, cols uint16) error {
	return c.do(ctx, "POST", "/profiles/"+url.PathEscape(id)+"/login", map[string]uint16{"rows": rows, "cols": cols}, nil)
}

// Employees lists the employees.
func (c *Client) Employees(ctx context.Context) ([]Employee, error) {
	var out []Employee
	return out, c.do(ctx, "GET", "/employees", nil, &out)
}

// Employee returns one employee.
func (c *Client) Employee(ctx context.Context, id string) (Employee, error) {
	var out Employee
	return out, c.do(ctx, "GET", "/employees/"+url.PathEscape(id), nil, &out)
}

// CreateEmployee makes a new employee.
func (c *Client) CreateEmployee(ctx context.Context, in NewEmployee) (Employee, error) {
	var out Employee
	return out, c.do(ctx, "POST", "/employees", in, &out)
}

// DeleteEmployee retires an employee.
func (c *Client) DeleteEmployee(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/employees/"+url.PathEscape(id), nil, nil)
}

// Pause stops an employee's work and keeps it from starting more.
func (c *Client) Pause(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/pause", nil, nil)
}

// Resume lets a paused employee work again.
func (c *Client) Resume(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/resume", nil, nil)
}

// Send gives an employee a prompt and returns the ID of the turn it started
// ("" for an employee in terminal mode, where the prompt is typed).
func (c *Client) Send(ctx context.Context, id, prompt string) (string, error) {
	var out struct {
		TurnID string `json:"turn_id"`
	}
	err := c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/turns", map[string]string{"prompt": prompt}, &out)
	return out.TurnID, err
}

// Cancel ends an employee's running turn at once.
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/cancel", nil, nil)
}

// Interrupt asks an employee's running turn to stop politely.
func (c *Client) Interrupt(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/interrupt", nil, nil)
}

// History lists an employee's turns, newest first. before (an RFC 3339 time)
// pages back.
func (c *Client) History(ctx context.Context, id string, limit int, before string) ([]Turn, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	var out []Turn
	return out, c.do(ctx, "GET", "/employees/"+url.PathEscape(id)+"/history?"+q.Encode(), nil, &out)
}

// TurnEvents returns the stored events of a turn.
func (c *Client) TurnEvents(ctx context.Context, turnID string) ([]Event, error) {
	var out []Event
	return out, c.do(ctx, "GET", "/turns/"+url.PathEscape(turnID)+"/events", nil, &out)
}

// StartTerminal launches an employee's program in a terminal the daemon keeps
// alive.
func (c *Client) StartTerminal(ctx context.Context, id string, rows, cols uint16) error {
	return c.do(ctx, "POST", "/employees/"+url.PathEscape(id)+"/terminal", map[string]uint16{"rows": rows, "cols": cols}, nil)
}

// StopTerminal ends an employee's terminal program on purpose.
func (c *Client) StopTerminal(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/employees/"+url.PathEscape(id)+"/terminal", nil, nil)
}

// Approvals lists the actions waiting for the user.
func (c *Client) Approvals(ctx context.Context) ([]Approval, error) {
	var out []Approval
	return out, c.do(ctx, "GET", "/approvals", nil, &out)
}

// Answer resolves an approval. remember is "none", "exact" or "kind".
func (c *Client) Answer(ctx context.Context, requestID string, allow bool, remember string) error {
	return c.do(ctx, "POST", "/approvals/"+url.PathEscape(requestID), map[string]any{"allow": allow, "remember": remember}, nil)
}

// Grants lists an employee's remembered decisions.
func (c *Client) Grants(ctx context.Context, employeeID string) ([]Grant, error) {
	var out []Grant
	return out, c.do(ctx, "GET", "/employees/"+url.PathEscape(employeeID)+"/grants", nil, &out)
}

// SetGrant stores a remembered decision directly.
func (c *Client) SetGrant(ctx context.Context, employeeID string, g Grant) error {
	return c.do(ctx, "PUT", "/employees/"+url.PathEscape(employeeID)+"/grants", g, nil)
}

// DeleteGrant forgets a remembered decision.
func (c *Client) DeleteGrant(ctx context.Context, employeeID, kind, target string) error {
	q := url.Values{"kind": {kind}, "target": {target}}
	return c.do(ctx, "DELETE", "/employees/"+url.PathEscape(employeeID)+"/grants?"+q.Encode(), nil, nil)
}

// SearchMemory searches an employee's facts and notes.
func (c *Client) SearchMemory(ctx context.Context, employeeID, query string, limit int) ([]Hit, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	var out []Hit
	return out, c.do(ctx, "GET", "/employees/"+url.PathEscape(employeeID)+"/memory/search?"+q.Encode(), nil, &out)
}

// AddFact remembers a fact for an employee.
func (c *Client) AddFact(ctx context.Context, employeeID, key, body string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.do(ctx, "POST", "/employees/"+url.PathEscape(employeeID)+"/memory/facts", map[string]string{"key": key, "body": body}, &out)
	return out.ID, err
}

// Facts lists an employee's facts.
func (c *Client) Facts(ctx context.Context, employeeID string) ([]Fact, error) {
	var out []Fact
	return out, c.do(ctx, "GET", "/employees/"+url.PathEscape(employeeID)+"/memory/facts", nil, &out)
}

// DeleteFact forgets a fact.
func (c *Client) DeleteFact(ctx context.Context, employeeID string, factID int64) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/employees/%s/memory/facts/%d", url.PathEscape(employeeID), factID), nil, nil)
}
