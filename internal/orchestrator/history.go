package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"aotus/internal/provider"
	"aotus/internal/store"
)

// storedEvent is how a provider event is written to the history.
type storedEvent struct {
	Text      string          `json:"text,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Code      string          `json:"code,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolOut   string          `json:"tool_output,omitempty"`
	ToolError bool            `json:"tool_error,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// textFlushBytes is how much text piles up before it is written out.
const textFlushBytes = 8 * 1024

// turnWriter writes the events of one turn to the history, in order, joining
// consecutive text pieces into one entry so that a long answer is not a
// thousand rows.
type turnWriter struct {
	st     *store.Store
	turnID string
	text   strings.Builder
}

func (w *turnWriter) add(ev provider.Event) {
	if ev.Kind == provider.EventText {
		w.text.WriteString(ev.Text)
		if w.text.Len() >= textFlushBytes {
			w.flush()
		}
		return
	}
	w.flush() // keep the order: text that came before this event is written first
	se := storedEvent{Text: ev.Text, SessionID: ev.SessionID, Code: string(ev.Code)}
	if ev.Tool != nil {
		se.ToolID, se.ToolName, se.ToolInput, se.ToolOut, se.ToolError = ev.Tool.ID, ev.Tool.Name, ev.Tool.Input, ev.Tool.Output, ev.Tool.IsError
	}
	if ev.Done != nil {
		se.Reason = string(ev.Done.Reason)
	}
	w.write(string(ev.Kind), se)
}

func (w *turnWriter) flush() {
	if w.text.Len() == 0 {
		return
	}
	w.write(string(provider.EventText), storedEvent{Text: w.text.String()})
	w.text.Reset()
}

func (w *turnWriter) write(kind string, se storedEvent) {
	b, _ := json.Marshal(se)
	// The turn may have been canceled; its record must still be completed.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = w.st.AddTurnEvent(ctx, w.turnID, kind, string(b))
}

// Turn is a request to an employee and how it ended.
type Turn struct {
	ID           string
	EmployeeID   string
	Prompt       string
	State        string
	Error        string
	StartedAt    time.Time
	EndedAt      time.Time
	CostUSD      float64
	InputTokens  int
	OutputTokens int
}

// HistoryEvent is one stored event of a turn.
type HistoryEvent struct {
	Seq   int64
	Kind  provider.EventKind
	Event provider.Event
}

// History returns an employee's turns, newest first. Pass the StartedAt of the
// oldest turn you already have as before to load the page behind it: the UI
// loads history on demand.
func (m *Manager) History(ctx context.Context, employeeID string, limit int, before time.Time) ([]Turn, error) {
	rows, err := m.st.Turns(ctx, employeeID, limit, before)
	if err != nil {
		return nil, err
	}
	out := make([]Turn, len(rows))
	for i, r := range rows {
		out[i] = Turn(r)
	}
	return out, nil
}

// TurnEvents returns the stored events of a turn, in order.
func (m *Manager) TurnEvents(ctx context.Context, turnID string) ([]HistoryEvent, error) {
	rows, err := m.st.TurnEvents(ctx, turnID)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEvent, 0, len(rows))
	for _, r := range rows {
		var se storedEvent
		if json.Unmarshal([]byte(r.Payload), &se) != nil {
			continue
		}
		ev := provider.Event{Kind: provider.EventKind(r.Kind), Text: se.Text, SessionID: se.SessionID, Code: provider.ErrorCode(se.Code)}
		if se.ToolID != "" {
			ev.Tool = &provider.Tool{ID: se.ToolID, Name: se.ToolName, Input: se.ToolInput, Output: se.ToolOut, IsError: se.ToolError}
		}
		if se.Reason != "" {
			ev.Done = &provider.Done{Reason: provider.DoneReason(se.Reason)}
		}
		out = append(out, HistoryEvent{Seq: r.Seq, Kind: ev.Kind, Event: ev})
	}
	return out, nil
}
