package permissions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"aotus/internal/store"
)

// Kind classifies what an employee wants to do.
type Kind string

// Kinds of action. Everything that is not known to be safe is sensitive.
const (
	// ReadInside and WriteInside work on the employee's own folder.
	ReadInside  Kind = "read_inside"
	WriteInside Kind = "write_inside"
	// The rest leave the employee's folder or reach out of the machine.
	ReadOutside  Kind = "read_outside"
	WriteOutside Kind = "write_outside"
	RunCommand   Kind = "run_command"
	Network      Kind = "network"
)

// Sensitive reports whether an action of this kind needs approval. Only the
// two kinds that stay inside the employee's folder are safe; a kind this code
// does not know is sensitive too.
func (k Kind) Sensitive() bool { return k != ReadInside && k != WriteInside }

// Action is something an employee wants to do.
type Action struct {
	EmployeeID string
	Kind       Kind
	// Target is the exact thing: a command line, a path, a host.
	Target string
}

// Decision is the outcome for an action.
type Decision string

// Decisions.
const (
	Allowed Decision = "allowed"
	Denied  Decision = "denied"
)

// Reason says how a decision was reached; it is written to the audit log.
type Reason string

// Reasons.
const (
	ReasonSafe       Reason = "safe"
	ReasonRemembered Reason = "remembered"
	ReasonUser       Reason = "user"
	ReasonTimeout    Reason = "timeout"
	ReasonCanceled   Reason = "canceled"
	ReasonAuditError Reason = "audit_unavailable"
)

// Remember says how long an answer from the user applies.
type Remember int

// What to remember.
const (
	// RememberNone applies the answer to this request only.
	RememberNone Remember = iota
	// RememberExact applies it to this employee, this kind and this target.
	RememberExact
	// RememberKind applies it to this employee and every target of this kind.
	RememberKind
)

// Request is a pending approval shown to the user.
type Request struct {
	ID     string
	Action Action
	At     time.Time
}

// Errors returned by Answer.
var (
	// ErrUnknownRequest means the request is not pending (answered, expired or
	// canceled already).
	ErrUnknownRequest = errors.New("permissions: no such pending request")
)

// DefaultTimeout is how long a request waits for the user. An unanswered
// request is denied: the safe outcome.
const DefaultTimeout = 15 * time.Minute

// Broker decides whether actions may happen. Safe actions pass; remembered
// decisions apply; everything else waits for the user and is denied if nobody
// answers. Every decision about a sensitive action is written to the audit log
// first, and if that fails the action is denied.
type Broker struct {
	st *store.Store
	// Timeout is how long a request waits; zero means DefaultTimeout.
	Timeout time.Duration
	clock   func() time.Time

	mu      sync.Mutex
	pending map[string]*pending
	subs    map[int]chan Request
	nextSub int
}

type pending struct {
	req    Request
	answer chan answer
}

type answer struct {
	allow    bool
	remember Remember
	by       string // who answered, for the audit log
}

// New returns a broker that records to st.
func New(st *store.Store) *Broker {
	return &Broker{st: st, clock: time.Now, pending: map[string]*pending{}, subs: map[int]chan Request{}}
}

func (b *Broker) timeout() time.Duration {
	if b.Timeout > 0 {
		return b.Timeout
	}
	return DefaultTimeout
}

// Check decides on an action. It blocks while a sensitive action waits for the
// user, and returns when the user answers, the timeout passes or ctx is done.
// The error is non-nil only if the decision itself failed (never for a denial).
func (b *Broker) Check(ctx context.Context, a Action) (Decision, Reason, error) {
	if !a.Kind.Sensitive() {
		return Allowed, ReasonSafe, nil
	}
	if a.EmployeeID == "" {
		return Denied, ReasonUser, errors.New("permissions: an action needs an employee")
	}

	// A remembered decision for exactly this employee, kind and target, or for
	// every target of this kind, applies. An exact entry wins over a kind-wide
	// one, so "allow all of this kind" does not override a specific "deny".
	for _, target := range []string{a.Target, "*"} {
		g, ok, err := b.st.Grant(ctx, a.EmployeeID, string(a.Kind), target)
		if err != nil {
			return Denied, ReasonAuditError, fmt.Errorf("permissions: reading remembered decisions: %w", err)
		}
		if ok {
			return b.finish(ctx, a, g.Allow, ReasonRemembered, "")
		}
	}

	p := &pending{
		req:    Request{ID: newRequestID(), Action: a, At: b.clock()},
		answer: make(chan answer, 1),
	}
	b.mu.Lock()
	b.pending[p.req.ID] = p
	b.mu.Unlock()
	b.notify(p.req)
	defer func() {
		b.mu.Lock()
		delete(b.pending, p.req.ID)
		b.mu.Unlock()
	}()

	timer := time.NewTimer(b.timeout())
	defer timer.Stop()
	select {
	case ans := <-p.answer:
		if ans.remember != RememberNone {
			target := a.Target
			if ans.remember == RememberKind {
				target = "*"
			}
			if err := b.st.PutGrant(ctx, store.GrantRow{EmployeeID: a.EmployeeID, Kind: string(a.Kind), Target: target, Allow: ans.allow, CreatedAt: b.clock()}); err != nil {
				return Denied, ReasonAuditError, fmt.Errorf("permissions: remembering the decision: %w", err)
			}
		}
		return b.finish(ctx, a, ans.allow, ReasonUser, ans.by)
	case <-timer.C:
		return b.finish(ctx, a, false, ReasonTimeout, "")
	case <-ctx.Done():
		// The turn was canceled while waiting: record it, even though ctx is
		// done, so the log shows the request that was abandoned.
		return b.finish(context.WithoutCancel(ctx), a, false, ReasonCanceled, "")
	}
}

// finish writes the audit entry and only then reports the decision. If the
// entry cannot be written, an allow becomes a deny: no action may happen
// without a record.
func (b *Broker) finish(ctx context.Context, a Action, allow bool, why Reason, by string) (Decision, Reason, error) {
	decision := Denied
	if allow {
		decision = Allowed
	}
	_, err := b.st.AppendAudit(ctx, store.AuditRow{
		At: b.clock(), EmployeeID: a.EmployeeID, Kind: "permission", Action: string(a.Kind),
		Detail: a.Target + " [" + string(why) + "]", Decision: string(decision), Caller: by,
	})
	if err != nil {
		return Denied, ReasonAuditError, fmt.Errorf("permissions: the audit log is unavailable, so the action was denied: %w", err)
	}
	return decision, why, nil
}

// Remember stores a decision without waiting for a question, for example from
// the employee's settings screen. target "*" covers every target of the kind
// for that employee. An exact target always wins over "*", so a specific deny
// can sit next to a broad allow.
func (b *Broker) Remember(ctx context.Context, employeeID string, kind Kind, target string, allow bool) error {
	if employeeID == "" || target == "" {
		return errors.New("permissions: a remembered decision needs an employee and a target")
	}
	decision := "denied"
	if allow {
		decision = "allowed"
	}
	if _, err := b.st.AppendAudit(ctx, store.AuditRow{At: b.clock(), EmployeeID: employeeID, Kind: "permission",
		Action: string(kind), Detail: target + " [remembered by the user]", Decision: decision}); err != nil {
		return fmt.Errorf("permissions: the audit log is unavailable, so nothing was changed: %w", err)
	}
	return b.st.PutGrant(ctx, store.GrantRow{EmployeeID: employeeID, Kind: string(kind), Target: target, Allow: allow, CreatedAt: b.clock()})
}

// Forget removes a remembered decision.
func (b *Broker) Forget(ctx context.Context, employeeID string, kind Kind, target string) error {
	if _, err := b.st.AppendAudit(ctx, store.AuditRow{At: b.clock(), EmployeeID: employeeID, Kind: "permission",
		Action: string(kind), Detail: target + " [forgotten by the user]", Decision: "forgotten"}); err != nil {
		return fmt.Errorf("permissions: the audit log is unavailable, so nothing was changed: %w", err)
	}
	return b.st.DeleteGrant(ctx, employeeID, string(kind), target)
}

// Remembered lists the decisions remembered for an employee.
func (b *Broker) Remembered(ctx context.Context, employeeID string) ([]store.GrantRow, error) {
	return b.st.Grants(ctx, employeeID)
}

// Answer resolves a pending request.
func (b *Broker) Answer(requestID string, allow bool, remember Remember) error {
	return b.AnswerAs(requestID, allow, remember, "")
}

// AnswerAs is Answer that records who answered in the audit log.
func (b *Broker) AnswerAs(requestID string, allow bool, remember Remember, by string) error {
	b.mu.Lock()
	p, ok := b.pending[requestID]
	b.mu.Unlock()
	if !ok {
		return ErrUnknownRequest
	}
	select {
	case p.answer <- answer{allow: allow, remember: remember, by: by}:
		return nil
	default:
		return ErrUnknownRequest // answered already
	}
}

// Pending lists the requests waiting for the user, oldest first.
func (b *Broker) Pending() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Request, 0, len(b.pending))
	for _, p := range b.pending {
		out = append(out, p.req)
	}
	sortRequests(out)
	return out
}

// Subscribe delivers new requests as they appear, for a UI that shows
// approval prompts. A subscriber that does not keep up misses requests (it
// can always call Pending); it never slows the agents down.
func (b *Broker) Subscribe() (<-chan Request, func()) {
	ch := make(chan Request, 64)
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *Broker) notify(r Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- r:
		default:
		}
	}
}

func sortRequests(rs []Request) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].At.Before(rs[j-1].At); j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

func newRequestID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "req-" + hex.EncodeToString(b[:])
}
