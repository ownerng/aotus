package api

import (
	"encoding/json"
	"time"

	"aotus/internal/orchestrator"
)

func kindOf(s string) orchestrator.Kind { return orchestrator.Kind(s) }
func modeOf(s string) orchestrator.Mode { return orchestrator.Mode(s) }

// eventDTO is a provider event on the wire.
type eventDTO struct {
	Seq       int64           `json:"seq,omitempty"` // history only
	Kind      string          `json:"kind"`
	SessionID string          `json:"session_id,omitempty"`
	Text      string          `json:"text,omitempty"`
	Code      string          `json:"code,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolOut   string          `json:"tool_output,omitempty"`
	ToolError bool            `json:"tool_error,omitempty"`
	Limits    *limitsDTO      `json:"limits,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	CostUSD   float64         `json:"cost_usd,omitempty"`
	InTokens  int             `json:"input_tokens,omitempty"`
	OutTokens int             `json:"output_tokens,omitempty"`
}

type limitsDTO struct {
	Status   string `json:"status"`
	Window   string `json:"window"`
	ResetsAt string `json:"resets_at,omitempty"`
}

func toEventDTO(e *orchestrator.Event) eventDTO {
	d := eventDTO{Kind: string(e.Kind), SessionID: e.SessionID, Text: e.Text, Code: string(e.Code)}
	if t := e.Tool; t != nil {
		d.ToolID, d.ToolName, d.ToolInput, d.ToolOut, d.ToolError = t.ID, t.Name, t.Input, t.Output, t.IsError
	}
	if l := e.Limits; l != nil {
		d.Limits = &limitsDTO{Status: l.Status, Window: l.Window}
		if !l.ResetsAt.IsZero() {
			d.Limits.ResetsAt = l.ResetsAt.UTC().Format(time.RFC3339)
		}
	}
	if e.Done != nil {
		d.Reason, d.CostUSD, d.InTokens, d.OutTokens = string(e.Done.Reason), e.Done.CostUSD, e.Done.InputTokens, e.Done.OutputTokens
	}
	return d
}

// updateDTO is an orchestrator update on the wire.
type updateDTO struct {
	Seq        uint64    `json:"seq"`
	Kind       string    `json:"kind"`
	EmployeeID string    `json:"employee_id"`
	TurnID     string    `json:"turn_id,omitempty"`
	State      string    `json:"state,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Event      *eventDTO `json:"event,omitempty"`
}

func toUpdateDTO(u orchestrator.Update) updateDTO {
	d := updateDTO{Seq: u.Seq, Kind: string(u.Kind), EmployeeID: u.EmployeeID, TurnID: u.TurnID, State: u.State, Detail: u.Detail}
	if u.Event != nil {
		e := toEventDTO(u.Event)
		d.Event = &e
	}
	return d
}
