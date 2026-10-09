package orchestrator

import (
	"sync"
	"sync/atomic"

	"aotus/internal/provider"
)

// UpdateKind says what an Update reports.
type UpdateKind string

// Kinds of update.
const (
	UpdateTurnQueued  UpdateKind = "turn_queued"
	UpdateTurnStarted UpdateKind = "turn_started"
	// UpdateEvent carries one normalized provider event of a running turn.
	UpdateEvent     UpdateKind = "event"
	UpdateTurnEnded UpdateKind = "turn_ended"
	// UpdateSession reports the state of an employee's terminal session.
	UpdateSession UpdateKind = "session"
)

// Terminal session states reported in Update.State.
const (
	SessionRunning    = "running"
	SessionRestarting = "restarting"
	SessionStopped    = "stopped"
	SessionCrashed    = "crashed"
)

// Update is one thing that happened, delivered to subscribers in order.
type Update struct {
	// Seq increases by one for every update the manager publishes, so a client
	// can tell it missed some.
	Seq        uint64
	Kind       UpdateKind
	EmployeeID string
	TurnID     string
	Event      *provider.Event // UpdateEvent
	State      string          // UpdateTurnEnded: the turn state; UpdateSession: the session state
	Detail     string
}

// hub delivers updates to subscribers without ever waiting for them.
type hub struct {
	seq  atomic.Uint64
	mu   sync.Mutex
	subs map[int]*subscriber
	next int
	buf  int
}

type subscriber struct {
	employeeID string // "" means every employee
	ch         chan Update
}

func newHub(buffer int) *hub { return &hub{subs: map[int]*subscriber{}, buf: buffer} }

// subscribe returns a channel of updates for one employee (or all, with ""). A
// subscriber that falls more than the buffer behind is disconnected: its
// channel is closed, and it can subscribe again and reload what it missed from
// the history. The agents are never slowed down by a viewer.
func (h *hub) subscribe(employeeID string) (<-chan Update, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &subscriber{employeeID: employeeID, ch: make(chan Update, h.buf)}
	id := h.next
	h.next++
	h.subs[id] = s
	return s.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if cur, ok := h.subs[id]; ok {
			close(cur.ch)
			delete(h.subs, id)
		}
	}
}

func (h *hub) publish(u Update) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u.Seq = h.seq.Add(1)
	for id, s := range h.subs {
		if s.employeeID != "" && s.employeeID != u.EmployeeID {
			continue
		}
		select {
		case s.ch <- u:
		default:
			close(s.ch)
			delete(h.subs, id)
		}
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.subs {
		close(s.ch)
		delete(h.subs, id)
	}
}
