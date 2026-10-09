package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"aotus/internal/provider"
	"aotus/internal/store"
	"aotus/internal/workspace"
)

// Errors returned by the service. Use errors.Is.
var (
	// ErrNameTaken means a live employee already has this name (or one that
	// gives the same folder name).
	ErrNameTaken = errors.New("an employee with this name already exists")
	// ErrNoProfile means the profile an employee should use does not exist.
	ErrNoProfile = errors.New("profile does not exist")
	// ErrInvalid means a field is missing or out of range.
	ErrInvalid = errors.New("invalid employee")
	// ErrNotFound means the employee does not exist (or was deleted).
	ErrNotFound = errors.New("employee not found")
	// ErrProfileInUse means employees still use the profile.
	ErrProfileInUse = errors.New("profile is used by employees")
)

// Limits on free text, so one request cannot fill the database.
const (
	maxName   = 80
	maxRole   = 200
	maxPrompt = 20000
)

// State of an employee.
type State string

// Employee states.
const (
	StateActive State = store.StateActive
	StatePaused State = store.StatePaused
)

// Employee is a persistent agent: a name, a job, and one profile to work with.
type Employee struct {
	ID             string
	Slug           string
	Name           string
	Role           string
	SystemPrompt   string
	ProfileID      string
	State          State
	PermissionMode string
	AllowedTools   []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewEmployee is the input of CreateEmployee.
type NewEmployee struct {
	Name           string
	Role           string
	SystemPrompt   string
	ProfileID      string
	PermissionMode string
	AllowedTools   []string
}

// Service manages employees and profiles. It is safe for concurrent use.
type Service struct {
	st    *store.Store
	ws    *workspace.Manager
	clock func() time.Time
}

// New returns a service over a store and a workspace manager.
func New(st *store.Store, ws *workspace.Manager) *Service {
	return &Service{st: st, ws: ws, clock: time.Now}
}

// AddProfile validates and stores a subscription profile and creates its
// isolated configuration directory. It stores no secret: an API key lives in
// the operating system credential store and the profile only names it.
func (s *Service) AddProfile(ctx context.Context, p provider.Profile) error {
	if err := p.Validate(s.st.Layout()); err != nil {
		return err
	}
	if err := p.EnsureConfigDir(); err != nil {
		return err
	}
	return s.st.PutProfile(ctx, store.ProfileRow{
		ID: p.ID, Name: p.Name, Kind: string(p.Kind), Binary: p.Binary, ConfigDir: p.ConfigDir,
		Mode: string(p.Mode), Model: p.Model, BaseURL: p.BaseURL, APIKeyRef: p.APIKeyRef,
		TermsCheckedAt: p.TermsCheckedAt, AcceptedNotices: p.AcceptedNotices, ExtraEnv: p.ExtraEnv,
		CreatedAt: s.clock(),
	})
}

// Profile returns a stored profile.
func (s *Service) Profile(ctx context.Context, id string) (provider.Profile, error) {
	row, err := s.st.Profile(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return provider.Profile{}, fmt.Errorf("%w: %s", ErrNoProfile, id)
	}
	if err != nil {
		return provider.Profile{}, err
	}
	return profileFromRow(row), nil
}

// Profiles lists the stored profiles.
func (s *Service) Profiles(ctx context.Context) ([]provider.Profile, error) {
	rows, err := s.st.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Profile, len(rows))
	for i, r := range rows {
		out[i] = profileFromRow(r)
	}
	return out, nil
}

// RemoveProfile deletes a profile that no employee uses. Its configuration
// directory (and the login inside it) is left on disk for the user to remove.
func (s *Service) RemoveProfile(ctx context.Context, id string) error {
	err := s.st.DeleteProfile(ctx, id)
	switch {
	case errors.Is(err, store.ErrInUse):
		return fmt.Errorf("%w: %s", ErrProfileInUse, id)
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNoProfile, id)
	}
	return err
}

func profileFromRow(r store.ProfileRow) provider.Profile {
	return provider.Profile{
		ID: r.ID, Name: r.Name, Kind: provider.Kind(r.Kind), Binary: r.Binary, ConfigDir: r.ConfigDir,
		Mode: provider.Mode(r.Mode), Model: r.Model, BaseURL: r.BaseURL, APIKeyRef: r.APIKeyRef,
		TermsCheckedAt: r.TermsCheckedAt, AcceptedNotices: r.AcceptedNotices, ExtraEnv: r.ExtraEnv,
	}
}

func (s *Service) audit(ctx context.Context, employeeID, action, detail string) {
	// The audit log is best effort here: failing to record must not undo a
	// change that already happened. The permissions layer treats it as
	// mandatory for approvals.
	_, _ = s.st.AppendAudit(ctx, store.AuditRow{At: s.clock(), EmployeeID: employeeID, Kind: "employee", Action: action, Detail: detail})
}

// CreateEmployee creates an employee with its own folder, bound to one
// existing profile.
func (s *Service) CreateEmployee(ctx context.Context, in NewEmployee) (Employee, error) {
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return Employee{}, fmt.Errorf("%w: a name is required", ErrInvalid)
	case len([]rune(name)) > maxName:
		return Employee{}, fmt.Errorf("%w: the name is longer than %d characters", ErrInvalid, maxName)
	case len([]rune(in.Role)) > maxRole:
		return Employee{}, fmt.Errorf("%w: the role is longer than %d characters", ErrInvalid, maxRole)
	case len([]rune(in.SystemPrompt)) > maxPrompt:
		return Employee{}, fmt.Errorf("%w: the system prompt is longer than %d characters", ErrInvalid, maxPrompt)
	case in.ProfileID == "":
		return Employee{}, fmt.Errorf("%w: choose the profile the employee works with", ErrInvalid)
	}
	slug, err := workspace.Slug(name)
	if err != nil {
		return Employee{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := s.st.Profile(ctx, in.ProfileID); errors.Is(err, store.ErrNotFound) {
		return Employee{}, fmt.Errorf("%w: %s", ErrNoProfile, in.ProfileID)
	} else if err != nil {
		return Employee{}, err
	}

	folder, err := s.ws.Dir(slug)
	if err != nil {
		return Employee{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	_, statErr := os.Stat(folder) // nil means the folder was already there
	now := s.clock()
	row := store.EmployeeRow{
		ID: newID(), Slug: slug, Name: name, Role: strings.TrimSpace(in.Role), SystemPrompt: in.SystemPrompt,
		ProfileID: in.ProfileID, State: store.StateActive, PermissionMode: in.PermissionMode,
		AllowedTools: in.AllowedTools, CreatedAt: now, UpdatedAt: now,
	}
	// Claim the name first: the unique index decides races, and a failed
	// claim leaves nothing on disk.
	if err := s.st.CreateEmployee(ctx, row); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return Employee{}, fmt.Errorf("%w: %q", ErrNameTaken, name)
		}
		return Employee{}, err
	}
	if _, err := s.ws.Create(slug); err != nil {
		_ = s.st.SetEmployeeState(ctx, row.ID, store.StateDeleted, now)
		if statErr != nil { // it did not exist, so we created it: remove what we made
			_ = os.RemoveAll(folder)
		}
		return Employee{}, err
	}
	s.audit(ctx, row.ID, "created", fmt.Sprintf("name=%q profile=%s", name, in.ProfileID))
	return fromRow(row), nil
}

// Employee returns one employee that is not deleted.
func (s *Service) Employee(ctx context.Context, id string) (Employee, error) {
	row, err := s.st.Employee(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.State == store.StateDeleted) {
		return Employee{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Employee{}, err
	}
	return fromRow(row), nil
}

// Employees lists the employees that are not deleted.
func (s *Service) Employees(ctx context.Context) ([]Employee, error) {
	rows, err := s.st.Employees(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]Employee, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// Pause marks an employee as paused. Stopping its running session is the job
// of the session manager; this records the intent so that it survives a
// restart.
func (s *Service) Pause(ctx context.Context, id string) error {
	return s.setState(ctx, id, store.StatePaused, "paused")
}

// Resume marks a paused employee as active again.
func (s *Service) Resume(ctx context.Context, id string) error {
	return s.setState(ctx, id, store.StateActive, "resumed")
}

func (s *Service) setState(ctx context.Context, id, state, action string) error {
	if err := s.st.SetEmployeeState(ctx, id, state, s.clock()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	s.audit(ctx, id, action, "")
	return nil
}

// DeleteEmployee retires an employee: its folder goes to the trash (never
// deleted outright) and the record stays, marked deleted, so that its history
// and audit log keep their meaning. The name can be used again.
func (s *Service) DeleteEmployee(ctx context.Context, id string) error {
	row, err := s.st.Employee(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.State == store.StateDeleted) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if err := s.st.SetEmployeeState(ctx, id, store.StateDeleted, s.clock()); err != nil {
		return err
	}
	trash, moveErr := s.ws.Remove(row.Slug)
	switch {
	case moveErr != nil && errors.Is(moveErr, os.ErrNotExist):
		s.audit(ctx, id, "deleted", "folder was already gone")
		return nil
	case moveErr != nil:
		s.audit(ctx, id, "deleted", "the folder could not be moved to the trash: "+moveErr.Error())
		return fmt.Errorf("the employee was deleted but its folder could not be moved to the trash: %w", moveErr)
	}
	s.audit(ctx, id, "deleted", "folder moved to "+trash)
	return nil
}

// Audit returns the audit entries of one employee, oldest first.
func (s *Service) Audit(ctx context.Context, employeeID string) ([]store.AuditRow, error) {
	return s.st.Audit(ctx, employeeID, 0)
}

func fromRow(r store.EmployeeRow) Employee {
	return Employee{
		ID: r.ID, Slug: r.Slug, Name: r.Name, Role: r.Role, SystemPrompt: r.SystemPrompt,
		ProfileID: r.ProfileID, State: State(r.State), PermissionMode: r.PermissionMode, AllowedTools: r.AllowedTools,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "emp-" + hex.EncodeToString(b[:])
}
