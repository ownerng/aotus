package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
	"aotus/internal/store"
)

const base = "/api/v1"

func (s *Server) register() {
	s.handle("GET "+base+"/status", s.status)

	s.handle("GET "+base+"/profiles", s.listProfiles)
	s.handleOwner("POST "+base+"/profiles", s.createProfile)
	s.handleOwner("DELETE "+base+"/profiles/{id}", s.deleteProfile)
	s.handle("GET "+base+"/profiles/{id}/detect", s.detectProfile)
	s.handleOwner("POST "+base+"/profiles/{id}/notices", s.acceptNotice)
	s.handleOwner("PUT "+base+"/profiles/{id}/api-key", s.setAPIKey)
	s.handleOwner("DELETE "+base+"/profiles/{id}/api-key", s.removeAPIKey)
	s.handleOwner("POST "+base+"/profiles/{id}/login", s.startLogin)
	s.handleOwner("GET "+base+"/profiles/{id}/login/ws", s.loginWS)

	s.handle("GET "+base+"/employees", s.listEmployees)
	s.handle("POST "+base+"/employees", s.createEmployee)
	s.handle("GET "+base+"/employees/{id}", s.getEmployee)
	s.handleEmployee("DELETE "+base+"/employees/{id}", s.deleteEmployee)
	s.handleEmployee("POST "+base+"/employees/{id}/pause", s.pauseEmployee)
	s.handleEmployee("POST "+base+"/employees/{id}/resume", s.resumeEmployee)

	s.handleEmployee("POST "+base+"/employees/{id}/turns", s.sendTurn)
	s.handleEmployee("POST "+base+"/employees/{id}/cancel", s.cancelTurn)
	s.handleEmployee("POST "+base+"/employees/{id}/interrupt", s.interruptTurn)
	s.handle("GET "+base+"/employees/{id}/history", s.history)
	s.handle("GET "+base+"/turns/{id}/events", s.turnEvents)
	s.handle("GET "+base+"/employees/{id}/audit", s.audit)

	s.handleEmployee("POST "+base+"/employees/{id}/terminal", s.startTerminal)
	s.handleEmployee("DELETE "+base+"/employees/{id}/terminal", s.stopTerminal)
	s.handleEmployee("GET "+base+"/employees/{id}/terminal/ws", s.terminalWS)

	s.handle("GET "+base+"/me", s.me)
	s.handleOwner("GET "+base+"/access", s.listAccess)
	s.handleOwner("POST "+base+"/access", s.addAccess)
	s.handleOwner("DELETE "+base+"/access/{login}", s.removeAccess)
	s.handleOwner("POST "+base+"/profiles/{id}/shares", s.shareProfile)
	s.handleOwner("DELETE "+base+"/profiles/{id}/shares/{login}", s.unshareProfile)

	s.handle("GET "+base+"/events", s.eventsWS)

	s.handle("GET "+base+"/approvals", s.listApprovals)
	s.handle("POST "+base+"/approvals/{id}", s.answerApproval)
	s.handle("GET "+base+"/employees/{id}/grants", s.listGrants)
	s.handleOwner("PUT "+base+"/employees/{id}/grants", s.putGrant)
	s.handleOwner("DELETE "+base+"/employees/{id}/grants", s.deleteGrant)

	s.handle("GET "+base+"/employees/{id}/memory/search", s.searchMemory)
	s.handle("GET "+base+"/employees/{id}/memory/facts", s.listFacts)
	s.handleEmployee("POST "+base+"/employees/{id}/memory/facts", s.addFact)
	s.handleEmployee("DELETE "+base+"/employees/{id}/memory/facts/{fact}", s.deleteFact)
	s.handleEmployee("POST "+base+"/employees/{id}/memory/sync", s.syncMemory)
}

// fail maps an error to an HTTP status and a stable error code. Errors this
// code does not know are logged in full and reported without detail.
func (s *Server) fail(w http.ResponseWriter, err error) {
	type rule struct {
		is     error
		status int
		code   string
	}
	for _, r := range []rule{
		{orchestrator.ErrNotFound, http.StatusNotFound, "not_found"},
		{orchestrator.ErrNoProfile, http.StatusNotFound, "profile_not_found"},
		{orchestrator.ErrNameTaken, http.StatusConflict, "name_taken"},
		{orchestrator.ErrInvalid, http.StatusBadRequest, "invalid"},
		{orchestrator.ErrBusy, http.StatusConflict, "busy"},
		{orchestrator.ErrPaused, http.StatusConflict, "paused"},
		{orchestrator.ErrNotTerminal, http.StatusConflict, "not_terminal"},
		{orchestrator.ErrNotRunning, http.StatusConflict, "not_running"},
		{orchestrator.ErrNoTurn, http.StatusConflict, "no_turn"},
		{orchestrator.ErrProfileInUse, http.StatusConflict, "profile_in_use"},
		{orchestrator.ErrNoProvider, http.StatusBadRequest, "no_provider"},
		{orchestrator.ErrNoticeRequired, http.StatusConflict, "notice_required"},
		{orchestrator.ErrUnsupportedMode, http.StatusBadRequest, "unsupported_mode"},
		{orchestrator.ErrUnknownNotice, http.StatusBadRequest, "unknown_notice"},
		{orchestrator.ErrNoKeyStore, http.StatusServiceUnavailable, "no_credential_store"},
		{orchestrator.ErrNoLogin, http.StatusBadRequest, "no_login_flow"},
		{orchestrator.ErrClosed, http.StatusServiceUnavailable, "shutting_down"},
		{permissions.ErrUnknownRequest, http.StatusNotFound, "request_not_found"},
	} {
		if errors.Is(err, r.is) {
			writeError(w, r.status, r.code, err.Error())
			return
		}
	}
	s.log.Error("api request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "something went wrong inside the daemon; details are in its log")
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	emps, err := s.cfg.Manager.Service().Employees(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.cfg.Version, "employees": len(emps)})
}

// ---- profiles ----

type profileDTO struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Binary          string            `json:"binary"`
	Mode            string            `json:"mode"`
	Model           string            `json:"model"`
	BaseURL         string            `json:"base_url,omitempty"`
	AcceptedNotices []string          `json:"accepted_notices"`
	ExtraEnv        map[string]string `json:"extra_env,omitempty"`
	TermsCheckedAt  string            `json:"terms_checked_at,omitempty"`
}

func toProfileDTO(p orchestrator.Profile) profileDTO {
	notices := p.AcceptedNotices
	if notices == nil {
		notices = []string{}
	}
	return profileDTO{
		ID: p.ID, Name: p.Name, Kind: string(p.Kind), Binary: p.Binary, Mode: string(p.Mode), Model: p.Model,
		BaseURL: p.BaseURL, AcceptedNotices: notices, ExtraEnv: p.ExtraEnv, TermsCheckedAt: p.TermsCheckedAt,
	}
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	ps, err := s.cfg.Manager.Service().Profiles(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]profileDTO, len(ps))
	for i, p := range ps {
		out[i] = toProfileDTO(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string            `json:"kind"`
		Name     string            `json:"name"`
		Binary   string            `json:"binary"`
		Mode     string            `json:"mode"`
		Model    string            `json:"model"`
		BaseURL  string            `json:"base_url"`
		ExtraEnv map[string]string `json:"extra_env"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	p, err := s.cfg.Manager.CreateProfile(r.Context(), orchestrator.NewProfile{
		Kind: kindOf(in.Kind), Name: in.Name, Binary: in.Binary, Mode: modeOf(in.Mode), Model: in.Model,
		BaseURL: in.BaseURL, ExtraEnv: in.ExtraEnv,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProfileDTO(p))
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.Service().RemoveProfile(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) detectProfile(w http.ResponseWriter, r *http.Request) {
	d, err := s.cfg.Manager.Detect(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	modes := make([]string, len(d.Modes))
	for i, m := range d.Modes {
		modes[i] = string(m)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": d.Installed, "version": d.Version, "version_ok": d.VersionOK,
		"modes": modes, "login": string(d.Login), "detail": d.Detail,
	})
}

func (s *Server) acceptNotice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Notice string `json:"notice"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.cfg.Manager.AcceptNotice(r.Context(), r.PathValue("id"), in.Notice); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) setAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.cfg.Manager.SetAPIKey(r.Context(), r.PathValue("id"), in.Key); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w) // the key is never echoed back
}

func (s *Server) removeAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.RemoveAPIKey(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

type sizeDTO struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

func (s *Server) startLogin(w http.ResponseWriter, r *http.Request) {
	var in sizeDTO
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	if _, err := s.cfg.Manager.LoginTerminal(r.Context(), r.PathValue("id"), in.Rows, in.Cols); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": true})
}

// ---- employees ----

type employeeDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	SystemPrompt    string   `json:"system_prompt"`
	ProfileID       string   `json:"profile_id"`
	State           string   `json:"state"`
	PermissionMode  string   `json:"permission_mode,omitempty"`
	AllowedTools    []string `json:"allowed_tools"`
	CreatedAt       string   `json:"created_at"`
	Mode            string   `json:"mode,omitempty"` // known once the session exists
	Working         bool     `json:"working"`
	TurnID          string   `json:"turn_id,omitempty"`
	TerminalRunning bool     `json:"terminal_running"`
}

func (s *Server) toEmployeeDTO(e orchestrator.Employee) employeeDTO {
	st := s.cfg.Manager.Status(e.ID)
	tools := e.AllowedTools
	if tools == nil {
		tools = []string{}
	}
	return employeeDTO{
		ID: e.ID, Name: e.Name, Role: e.Role, SystemPrompt: e.SystemPrompt, ProfileID: e.ProfileID, State: string(e.State),
		PermissionMode: e.PermissionMode, AllowedTools: tools, CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		Mode: string(st.Mode), Working: st.Working, TurnID: st.TurnID, TerminalRunning: st.TerminalRunning,
	}
}

func (s *Server) listEmployees(w http.ResponseWriter, r *http.Request) {
	es, err := s.cfg.Manager.Service().Employees(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]employeeDTO, len(es))
	for i, e := range es {
		out[i] = s.toEmployeeDTO(e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string   `json:"name"`
		Role           string   `json:"role"`
		SystemPrompt   string   `json:"system_prompt"`
		ProfileID      string   `json:"profile_id"`
		PermissionMode string   `json:"permission_mode"`
		AllowedTools   []string `json:"allowed_tools"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.requireProfile(w, r, in.ProfileID) {
		return
	}
	e, err := s.cfg.Manager.Service().CreateEmployee(r.Context(), orchestrator.NewEmployee{
		Name: in.Name, Role: in.Role, SystemPrompt: in.SystemPrompt, ProfileID: in.ProfileID,
		PermissionMode: in.PermissionMode, AllowedTools: in.AllowedTools,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toEmployeeDTO(e))
}

func (s *Server) getEmployee(w http.ResponseWriter, r *http.Request) {
	e, err := s.cfg.Manager.Service().Employee(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toEmployeeDTO(e))
}

func (s *Server) deleteEmployee(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) pauseEmployee(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.Pause(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) resumeEmployee(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.Resume(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

// ---- turns ----

func (s *Server) sendTurn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid", "the prompt is empty")
		return
	}
	id, err := s.cfg.Manager.Send(r.Context(), r.PathValue("id"), in.Prompt)
	if err != nil {
		s.fail(w, err)
		return
	}
	// A terminal-mode employee has no turns: the prompt was typed, turn_id is "".
	writeJSON(w, http.StatusAccepted, map[string]string{"turn_id": id})
}

func (s *Server) cancelTurn(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.CancelTurn(r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) interruptTurn(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.Interrupt(r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

type turnDTO struct {
	ID           string  `json:"id"`
	EmployeeID   string  `json:"employee_id"`
	Prompt       string  `json:"prompt"`
	State        string  `json:"state"`
	Error        string  `json:"error,omitempty"`
	StartedAt    string  `json:"started_at"`
	EndedAt      string  `json:"ended_at,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, err := parseTime(q.Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "before must be an RFC 3339 time")
		return
	}
	turns, err := s.cfg.Manager.History(r.Context(), r.PathValue("id"), limit, before)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]turnDTO, len(turns))
	for i, t := range turns {
		d := turnDTO{ID: t.ID, EmployeeID: t.EmployeeID, Prompt: t.Prompt, State: t.State, Error: t.Error,
			StartedAt: t.StartedAt.UTC().Format(time.RFC3339Nano), CostUSD: t.CostUSD, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens}
		if !t.EndedAt.IsZero() {
			d.EndedAt = t.EndedAt.UTC().Format(time.RFC3339Nano)
		}
		out[i] = d
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) turnEvents(w http.ResponseWriter, r *http.Request) {
	evs, err := s.cfg.Manager.TurnEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]eventDTO, len(evs))
	for i, e := range evs {
		out[i] = toEventDTO(&e.Event)
		out[i].Seq = e.Seq
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.cfg.Manager.Service().Audit(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	type row struct {
		At       string `json:"at"`
		Kind     string `json:"kind"`
		Action   string `json:"action"`
		Detail   string `json:"detail"`
		Decision string `json:"decision,omitempty"`
		Caller   string `json:"caller,omitempty"`
	}
	out := make([]row, len(rows))
	for i, a := range rows {
		out[i] = row{a.At.UTC().Format(time.RFC3339Nano), a.Kind, a.Action, a.Detail, a.Decision, a.Caller}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- terminal control ----

func (s *Server) startTerminal(w http.ResponseWriter, r *http.Request) {
	var in sizeDTO
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	if _, err := s.cfg.Manager.StartTerminal(r.Context(), r.PathValue("id"), in.Rows, in.Cols); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": true})
}

func (s *Server) stopTerminal(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Manager.StopTerminal(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

// ---- approvals and permissions ----

type requestDTO struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Kind       string `json:"kind"`
	Target     string `json:"target"`
	At         string `json:"at"`
}

func toRequestDTO(r permissions.Request) requestDTO {
	return requestDTO{ID: r.ID, EmployeeID: r.Action.EmployeeID, Kind: string(r.Action.Kind), Target: r.Action.Target, At: r.At.UTC().Format(time.RFC3339Nano)}
}

func (s *Server) listApprovals(w http.ResponseWriter, _ *http.Request) {
	pending := s.cfg.Broker.Pending()
	out := make([]requestDTO, len(pending))
	for i, p := range pending {
		out[i] = toRequestDTO(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) answerApproval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Allow    bool   `json:"allow"`
		Remember string `json:"remember"` // none, exact or kind
	}
	if !readJSON(w, r, &in) {
		return
	}
	var rem permissions.Remember
	switch in.Remember {
	case "", "none":
		rem = permissions.RememberNone
	case "exact":
		rem = permissions.RememberExact
	case "kind":
		rem = permissions.RememberKind
	default:
		writeError(w, http.StatusBadRequest, "invalid", `remember must be "none", "exact" or "kind"`)
		return
	}
	if p := principalOf(r); p.Role != store.RoleOwner {
		for _, req := range s.cfg.Broker.Pending() {
			if req.ID == r.PathValue("id") && !s.requireEmployee(w, r, req.Action.EmployeeID) {
				return
			}
		}
	}
	if err := s.cfg.Broker.AnswerAs(r.PathValue("id"), in.Allow, rem, principalOf(r).Caller.String()); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

type grantDTO struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Allow  bool   `json:"allow"`
}

func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) {
	rows, err := s.cfg.Broker.Remembered(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]grantDTO, len(rows))
	for i, g := range rows {
		out[i] = grantDTO{Kind: g.Kind, Target: g.Target, Allow: g.Allow}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putGrant(w http.ResponseWriter, r *http.Request) {
	var in grantDTO
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.cfg.Broker.Remember(r.Context(), r.PathValue("id"), permissions.Kind(in.Kind), in.Target, in.Allow); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	noContent(w)
}

func (s *Server) deleteGrant(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := s.cfg.Broker.Forget(r.Context(), r.PathValue("id"), permissions.Kind(q.Get("kind")), q.Get("target")); err != nil {
		s.fail(w, err)
		return
	}
	noContent(w)
}

// ---- memory ----

func (s *Server) searchMemory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := s.cfg.Manager.SearchMemory(r.Context(), r.PathValue("id"), r.URL.Query().Get("q"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	type hit struct {
		Kind    string `json:"kind"`
		Ref     string `json:"ref"`
		Snippet string `json:"snippet"`
	}
	out := make([]hit, len(hits))
	for i, h := range hits {
		out[i] = hit(h)
	}
	writeJSON(w, http.StatusOK, out)
}

type factDTO struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) listFacts(w http.ResponseWriter, r *http.Request) {
	facts, err := s.cfg.Manager.Facts(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]factDTO, len(facts))
	for i, f := range facts {
		out[i] = factDTO{f.ID, f.Key, f.Body, f.CreatedAt.UTC().Format(time.RFC3339)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) addFact(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string `json:"key"`
		Body string `json:"body"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.cfg.Manager.AddFact(r.Context(), r.PathValue("id"), in.Key, in.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) deleteFact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("fact"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "the fact ID is not a number")
		return
	}
	if err := s.cfg.Manager.DeleteFact(r.Context(), r.PathValue("id"), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such fact")
		return
	}
	noContent(w)
}

func (s *Server) syncMemory(w http.ResponseWriter, r *http.Request) {
	res, err := s.cfg.Manager.SyncMemory(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"indexed": res.Indexed, "removed": res.Removed, "skipped": res.Skipped})
}
