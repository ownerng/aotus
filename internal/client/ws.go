package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// Message is one frame of the events stream: a "hello", an "update" or an
// "approval".
type Message struct {
	Type     string    `json:"type"`
	Update   *Update   `json:"update,omitempty"`
	Approval *Approval `json:"approval,omitempty"`
}

// EventStream is a live connection to the daemon's events.
type EventStream struct {
	conn *websocket.Conn
}

func (c *Client) dial(ctx context.Context, path string) (*websocket.Conn, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.token)
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(c.base, "http")+path, &websocket.DialOptions{HTTPHeader: h}) //nolint:bodyclose // the websocket library owns and closes the response body
	if err != nil {
		if resp != nil && resp.StatusCode >= 400 {
			ae := &APIError{Status: resp.StatusCode, Code: fmt.Sprintf("http_%d", resp.StatusCode), Message: resp.Status}
			return nil, ae
		}
		return nil, err
	}
	conn.SetReadLimit(32 << 20)
	return conn, nil
}

// Events opens the stream of updates and approval requests; employeeID limits
// the updates to one employee ("" for all). The first message is a "hello".
func (c *Client) Events(ctx context.Context, employeeID string) (*EventStream, error) {
	path := "/events"
	if employeeID != "" {
		path += "?employee=" + url.QueryEscape(employeeID)
	}
	conn, err := c.dial(ctx, path)
	if err != nil {
		return nil, err
	}
	return &EventStream{conn: conn}, nil
}

// Next waits for the next message. It fails when the stream closes; a close
// with status 1013 means this client was too slow and should reconnect and
// reload the history.
func (s *EventStream) Next(ctx context.Context) (Message, error) {
	_, b, err := s.conn.Read(ctx)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, fmt.Errorf("client: unreadable message: %w", err)
	}
	return m, nil
}

// TooSlow reports whether err is the daemon disconnecting a slow client.
func TooSlow(err error) bool { return websocket.CloseStatus(err) == websocket.StatusTryAgainLater }

// Close ends the stream.
func (s *EventStream) Close() error { return s.conn.Close(websocket.StatusNormalClosure, "") }

// Terminal is a live connection to an employee's (or a login's) terminal.
type Terminal struct {
	conn *websocket.Conn
}

// Attach connects to the running terminal of an employee. The first data read
// is the recent output, so the screen can be redrawn.
func (c *Client) Attach(ctx context.Context, employeeID string) (*Terminal, error) {
	conn, err := c.dial(ctx, "/employees/"+url.PathEscape(employeeID)+"/terminal/ws")
	if err != nil {
		return nil, err
	}
	return &Terminal{conn: conn}, nil
}

// AttachLogin connects to the login flow started with StartLogin.
func (c *Client) AttachLogin(ctx context.Context, profileID string) (*Terminal, error) {
	conn, err := c.dial(ctx, "/profiles/"+url.PathEscape(profileID)+"/login/ws")
	if err != nil {
		return nil, err
	}
	return &Terminal{conn: conn}, nil
}

// Read returns the next piece of terminal output. It fails with a normal close
// when the program ended.
func (t *Terminal) Read(ctx context.Context) ([]byte, error) {
	_, b, err := t.conn.Read(ctx)
	return b, err
}

// Write sends typed input.
func (t *Terminal) Write(ctx context.Context, p []byte) error {
	return t.conn.Write(ctx, websocket.MessageBinary, p)
}

// Resize changes the terminal's size.
func (t *Terminal) Resize(ctx context.Context, rows, cols uint16) error {
	b, _ := json.Marshal(map[string]any{"type": "resize", "rows": rows, "cols": cols})
	return t.conn.Write(ctx, websocket.MessageText, b)
}

// Ended reports whether err means the program ended (or the viewer was too
// slow): the normal close the daemon sends.
func Ended(err error) bool { return websocket.CloseStatus(err) == websocket.StatusNormalClosure }

// Close detaches. The program keeps running.
func (t *Terminal) Close() error { return t.conn.Close(websocket.StatusNormalClosure, "") }
