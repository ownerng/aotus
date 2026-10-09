package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"aotus/internal/provider"
	"aotus/internal/store"
)

// Types of the provider layer that clients of the manager see. They are aliases
// so that the API can pass them along without depending on providers.
type (
	// Profile is a subscription profile.
	Profile = provider.Profile
	// Detection is what a provider found out about a profile's CLI.
	Detection = provider.Detection
	// Terminal is a running pseudo-terminal.
	Terminal = provider.Terminal
	// TerminalSession is a terminal-mode session.
	TerminalSession = provider.TerminalSession
	// Event is a normalized provider event.
	Event = provider.Event
	// Kind identifies a provider; Mode is how it is driven.
	Kind = provider.Kind
	Mode = provider.Mode
	// Fact is something an employee remembers.
	Fact = store.Fact
	// Hit is a memory search result.
	Hit = store.Hit
)

// KeyStore keeps API keys outside the database (the operating system's
// credential store in the daemon). Only the manager's SetAPIKey writes to it.
type KeyStore interface {
	Set(ref, value string) error
	Delete(ref string) error
}

// LoginProvider is implemented by providers whose CLI has its own login flow
// that can run in a terminal.
type LoginProvider interface {
	LoginSession(p provider.Profile) (provider.TerminalSession, error)
}

// Provider errors that clients of the manager tell apart.
var (
	ErrNoticeRequired     = provider.ErrNoticeRequired
	ErrUnsupportedMode    = provider.ErrUnsupportedMode
	ErrCLINotInstalled    = provider.ErrCLINotInstalled
	ErrNeedsLogin         = provider.ErrNeedsLogin
	ErrUnsupportedVersion = provider.ErrUnsupportedVersion
	ErrNoTurn             = provider.ErrNoTurn
)

// Errors of the facade.
var (
	// ErrUnknownNotice means the notice is not one Aotus defines.
	ErrUnknownNotice = errors.New("unknown notice")
	// ErrNoKeyStore means the daemon has no credential store configured.
	ErrNoKeyStore = errors.New("no credential store is configured")
	// ErrNoLogin means the profile's provider has no login flow.
	ErrNoLogin = errors.New("this kind of profile has no login flow")
)

// NewProfile is the input of CreateProfile.
type NewProfile struct {
	Kind     Kind
	Name     string
	Binary   string
	Mode     Mode
	Model    string
	BaseURL  string
	ExtraEnv map[string]string
}

// CreateProfile makes a new profile with its own isolated configuration
// directory and stores it.
func (m *Manager) CreateProfile(ctx context.Context, in NewProfile) (provider.Profile, error) {
	if _, ok := m.providers[in.Kind]; !ok {
		return provider.Profile{}, fmt.Errorf("%w: %s", ErrNoProvider, in.Kind)
	}
	p, err := provider.NewProfile(m.st.Layout(), in.Kind, in.Name, in.Binary)
	if err != nil {
		return provider.Profile{}, err
	}
	p.Mode, p.Model, p.BaseURL, p.ExtraEnv = in.Mode, in.Model, in.BaseURL, in.ExtraEnv
	if in.Kind == provider.KindOpenAI {
		p.APIKeyRef = "profile/" + p.ID + "/api-key"
	}
	if err := m.svc.AddProfile(ctx, p); err != nil {
		return provider.Profile{}, err
	}
	return p, nil
}

// Detect inspects the CLI of a profile: installed, version, modes and login.
func (m *Manager) Detect(ctx context.Context, profileID string) (provider.Detection, error) {
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return provider.Detection{}, err
	}
	prov, ok := m.providers[p.Kind]
	if !ok {
		return provider.Detection{}, fmt.Errorf("%w: %s", ErrNoProvider, p.Kind)
	}
	return prov.Detect(ctx, p)
}

// knownNotices are the notices a user can accept.
var knownNotices = []string{provider.NoticeClaudeHeadless}

// AcceptNotice records that the user accepted a notice for a profile. The text
// of the notice must have been shown to them.
func (m *Manager) AcceptNotice(ctx context.Context, profileID, notice string) error {
	if !slices.Contains(knownNotices, notice) {
		return fmt.Errorf("%w: %s", ErrUnknownNotice, notice)
	}
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return err
	}
	if slices.Contains(p.AcceptedNotices, notice) {
		return nil
	}
	p.AcceptedNotices = append(p.AcceptedNotices, notice)
	row, err := m.st.Profile(ctx, profileID)
	if err != nil {
		return err
	}
	row.AcceptedNotices = p.AcceptedNotices
	if err := m.st.PutProfile(ctx, row); err != nil {
		return err
	}
	_, _ = m.st.AppendAudit(ctx, store.AuditRow{Kind: "profile", Action: "notice accepted", Detail: profileID + " " + notice})
	m.dropSessions(profileID) // sessions created before keep the old rules
	return nil
}

// dropSessions closes the idle sessions built from a profile, so the next use
// picks up its new settings. Sessions in the middle of a turn are left alone.
func (m *Manager) dropSessions(profileID string) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.rts))
	for id := range m.rts {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		emp, err := m.svc.Employee(context.Background(), id)
		if err != nil || emp.ProfileID != profileID {
			continue
		}
		m.mu.Lock()
		rt := m.rts[id]
		m.mu.Unlock()
		if rt == nil {
			continue
		}
		rt.mu.Lock()
		idle := !rt.busy && !isRunning(rt.sess)
		sess := rt.sess
		if idle {
			rt.sess = nil
		}
		rt.mu.Unlock()
		if idle && sess != nil {
			_ = sess.Close()
		}
	}
}

func isRunning(s provider.Session) bool {
	if s == nil {
		return false
	}
	ts, ok := s.(provider.TerminalSession)
	return ok && ts.Terminal() != nil
}

// SetAPIKey stores a profile's API key in the credential store. The key is not
// kept anywhere else and is never returned.
func (m *Manager) SetAPIKey(ctx context.Context, profileID, key string) error {
	if m.opts.Keys == nil {
		return ErrNoKeyStore
	}
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return err
	}
	if p.APIKeyRef == "" {
		return fmt.Errorf("profile %s does not use an API key", profileID)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("the API key is empty")
	}
	if err := m.opts.Keys.Set(p.APIKeyRef, strings.TrimSpace(key)); err != nil {
		return err
	}
	_, _ = m.st.AppendAudit(ctx, store.AuditRow{Kind: "profile", Action: "api key stored", Detail: profileID})
	return nil
}

// RemoveAPIKey deletes a profile's API key from the credential store.
func (m *Manager) RemoveAPIKey(ctx context.Context, profileID string) error {
	if m.opts.Keys == nil {
		return ErrNoKeyStore
	}
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return err
	}
	if p.APIKeyRef == "" {
		return nil
	}
	return m.opts.Keys.Delete(p.APIKeyRef)
}

// LoginTerminal starts the profile's own login flow in a terminal, so the user
// signs in inside Aotus. Only one login runs per profile: starting another
// replaces the first. The caller reads the terminal of the returned session.
func (m *Manager) LoginTerminal(ctx context.Context, profileID string, rows, cols uint16) (provider.TerminalSession, error) {
	p, err := m.svc.Profile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	lp, ok := m.providers[p.Kind].(LoginProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoLogin, p.Kind)
	}
	sess, err := lp.LoginSession(p)
	if err != nil {
		return nil, err
	}
	if rows == 0 || cols == 0 {
		rows, cols = m.opts.TerminalRows, m.opts.TerminalCols
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = sess.Close()
		return nil, ErrClosed
	}
	old := m.logins[profileID]
	m.logins[profileID] = sess
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if err := sess.Launch(ctx, rows, cols); err != nil {
		m.mu.Lock()
		delete(m.logins, profileID)
		m.mu.Unlock()
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

// LoginSession returns the running login flow of a profile, if any.
func (m *Manager) LoginSession(profileID string) (provider.TerminalSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.logins[profileID]
	return s, ok
}

// memoryDir is where an employee's Markdown notes live.
func (m *Manager) memoryDir(ctx context.Context, employeeID string) (string, error) {
	emp, err := m.svc.Employee(ctx, employeeID)
	if err != nil {
		return "", err
	}
	dir, err := m.ws.Dir(emp.Slug)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "memory"), nil
}

// SyncMemory indexes the employee's Markdown notes (edits made by hand
// included) and returns what changed.
func (m *Manager) SyncMemory(ctx context.Context, employeeID string) (store.SyncResult, error) {
	dir, err := m.memoryDir(ctx, employeeID)
	if err != nil {
		return store.SyncResult{}, err
	}
	return m.st.SyncNotes(ctx, employeeID, dir)
}

// SearchMemory searches one employee's facts and notes.
func (m *Manager) SearchMemory(ctx context.Context, employeeID, query string, limit int) ([]store.Hit, error) {
	if _, err := m.svc.Employee(ctx, employeeID); err != nil {
		return nil, err
	}
	if _, err := m.SyncMemory(ctx, employeeID); err != nil { // pick up hand edits first
		return nil, err
	}
	return m.st.SearchMemory(ctx, employeeID, query, limit)
}

// AddFact remembers a fact for an employee.
func (m *Manager) AddFact(ctx context.Context, employeeID, key, body string) (int64, error) {
	if _, err := m.svc.Employee(ctx, employeeID); err != nil {
		return 0, err
	}
	return m.st.AddFact(ctx, employeeID, key, body, time.Now())
}

// Facts lists the facts of an employee.
func (m *Manager) Facts(ctx context.Context, employeeID string) ([]store.Fact, error) {
	if _, err := m.svc.Employee(ctx, employeeID); err != nil {
		return nil, err
	}
	return m.st.Facts(ctx, employeeID)
}

// DeleteFact forgets a fact of an employee.
func (m *Manager) DeleteFact(ctx context.Context, employeeID string, id int64) error {
	return m.st.DeleteFact(ctx, employeeID, id)
}
