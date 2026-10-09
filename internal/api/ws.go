package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"aotus/internal/orchestrator"
)

const (
	// maxFrame bounds a message from a client (typed input, a resize).
	maxFrame = 1 << 20
	// writeTimeout bounds one write to a client.
	writeTimeout = 10 * time.Second
)

// accept upgrades a request. The origin was already checked by ServeHTTP: a
// request with an Origin header never gets this far unless allow-listed, so the
// library's own origin check is switched off to let clients that are programs
// (which send no Origin) and allow-listed origins through.
func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxFrame)
	return c, nil
}

// message is one frame of the events stream.
type message struct {
	Type     string      `json:"type"` // "hello", "update" or "approval"
	Update   *updateDTO  `json:"update,omitempty"`
	Approval *requestDTO `json:"approval,omitempty"`
}

func send(ctx context.Context, c *websocket.Conn, typ websocket.MessageType, b []byte) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(ctx, typ, b)
}

// eventsWS streams updates (and approval requests) as they happen. The optional
// "employee" query parameter limits updates to one employee. A client too slow
// to keep up is disconnected with status 1013; it reconnects and reloads what
// it missed from the history.
func (s *Server) eventsWS(w http.ResponseWriter, r *http.Request) {
	updates, cancelUpdates := s.cfg.Manager.Subscribe(r.URL.Query().Get("employee"))
	defer cancelUpdates()
	approvals, cancelApprovals := s.cfg.Broker.Subscribe()
	defer cancelApprovals()

	c, err := accept(w, r)
	if err != nil {
		return // Accept already replied
	}
	defer func() { _ = c.CloseNow() }()
	ctx := c.CloseRead(r.Context()) // drains pings and notices the client leaving

	write := func(m message) bool {
		b, _ := json.Marshal(m)
		return send(ctx, c, websocket.MessageText, b) == nil
	}
	if !write(message{Type: "hello"}) {
		return
	}
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				_ = c.Close(websocket.StatusTryAgainLater, "too slow or the daemon is stopping; reconnect and reload the history")
				return
			}
			d := toUpdateDTO(u)
			if !write(message{Type: "update", Update: &d}) {
				return
			}
		case a, ok := <-approvals:
			if !ok {
				_ = c.Close(websocket.StatusTryAgainLater, "too slow; reconnect")
				return
			}
			d := toRequestDTO(a)
			if !write(message{Type: "approval", Approval: &d}) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) terminalWS(w http.ResponseWriter, r *http.Request) {
	term, err := s.cfg.Manager.Terminal(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	serveTerminal(w, r, term)
}

func (s *Server) loginWS(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.cfg.Manager.LoginSession(r.PathValue("id"))
	if !ok || sess.Terminal() == nil {
		s.fail(w, orchestrator.ErrNotRunning)
		return
	}
	serveTerminal(w, r, sess.Terminal())
}

// control is a text frame a terminal client sends.
type control struct {
	Type string `json:"type"` // "resize"
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// serveTerminal connects a client to a running terminal. Server to client: the
// recent output as the first binary frame(s), then the live output as binary
// frames. Client to server: binary frames are typed input; text frames are JSON
// controls ({"type":"resize","rows":N,"cols":N}). The terminal keeps running
// when the client leaves.
func serveTerminal(w http.ResponseWriter, r *http.Request, term orchestrator.Terminal) {
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	replay, live, unsubscribe := term.Subscribe()
	defer unsubscribe()
	if len(replay) > 0 {
		if send(ctx, c, websocket.MessageBinary, replay) != nil {
			return
		}
	}

	// Input from the client.
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, err := term.Write(data); err != nil {
					return
				}
			case websocket.MessageText:
				var ctl control
				if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Rows > 0 && ctl.Cols > 0 {
					_ = term.Resize(ctl.Rows, ctl.Cols)
				}
			}
		}
	}()

	for {
		select {
		case chunk, ok := <-live:
			if !ok {
				// The program ended, or this client fell behind.
				_ = c.Close(websocket.StatusNormalClosure, "the program ended or the viewer was too slow; reattach to continue")
				return
			}
			if send(ctx, c, websocket.MessageBinary, chunk) != nil {
				return
			}
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				_ = c.Close(websocket.StatusGoingAway, "")
			}
			return
		}
	}
}
