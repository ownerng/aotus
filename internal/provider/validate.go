package provider

import "fmt"

// ValidateTurn checks that the events of one turn follow the contract every
// provider must honor, and describes the first violation it finds.
//
//   - the turn ends with exactly one EventDone, and nothing comes after it;
//   - text events are not empty and error events have a code and a message;
//   - a failed turn has at least one error event, a completed one has none;
//   - a tool result answers an earlier tool request with the same ID;
//   - the session ID is reported at most once and is not empty.
func ValidateTurn(events []Event) error {
	if len(events) == 0 {
		return fmt.Errorf("turn has no events")
	}
	var errors, sessions, dones int
	requested := map[string]bool{}
	for i, e := range events {
		if dones > 0 {
			return fmt.Errorf("event %d (%s) comes after the done event", i, e.Kind)
		}
		switch e.Kind {
		case EventSession:
			sessions++
			if e.SessionID == "" {
				return fmt.Errorf("event %d: session event without an ID", i)
			}
		case EventText:
			if e.Text == "" {
				return fmt.Errorf("event %d: empty text event", i)
			}
		case EventToolRequest:
			if e.Tool == nil || e.Tool.ID == "" || e.Tool.Name == "" {
				return fmt.Errorf("event %d: tool request needs a tool with ID and name", i)
			}
			requested[e.Tool.ID] = true
		case EventToolResult:
			if e.Tool == nil || !requested[e.Tool.ID] {
				return fmt.Errorf("event %d: tool result without a matching request", i)
			}
		case EventLimits:
			if e.Limits == nil {
				return fmt.Errorf("event %d: limits event without limits", i)
			}
		case EventError:
			errors++
			if e.Code == "" || e.Text == "" {
				return fmt.Errorf("event %d: error event needs a code and a message", i)
			}
		case EventDone:
			dones++
			if e.Done == nil {
				return fmt.Errorf("event %d: done event without details", i)
			}
			switch e.Done.Reason {
			case DoneCompleted:
				if errors > 0 {
					return fmt.Errorf("turn completed but reported %d error event(s)", errors)
				}
			case DoneFailed:
				if errors == 0 {
					return fmt.Errorf("turn failed without any error event")
				}
			case DoneCanceled:
			default:
				return fmt.Errorf("event %d: unknown done reason %q", i, e.Done.Reason)
			}
		default:
			return fmt.Errorf("event %d: unknown kind %q", i, e.Kind)
		}
	}
	if sessions > 1 {
		return fmt.Errorf("session ID reported %d times", sessions)
	}
	if dones != 1 {
		return fmt.Errorf("turn must end with exactly one done event, found %d", dones)
	}
	return nil
}
