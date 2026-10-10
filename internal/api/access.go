package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"aotus/internal/store"
)

// SharingNotice is the text the owner acknowledges before another person is
// added. It is stored with the entry, so the record shows exactly what was
// accepted.
func SharingNotice(login string) string {
	return "I am letting " + login + " use this daemon. They can only use a provider subscription (Claude, ChatGPT/Codex...) " +
		"on the profiles I share with them one by one. Providers generally license a subscription to the person who pays for it: " +
		"letting someone else use it can break their terms and get the account limited or closed. Aotus cannot check this for me, " +
		"and I am responsible for allowing it."
}

// ---- guards ----

// handleOwner registers a route only the owner may use (the local token
// counts as the owner).
func (s *Server) handleOwner(pattern string, h http.HandlerFunc) {
	s.handle(pattern, func(w http.ResponseWriter, r *http.Request) {
		if !s.requireOwner(w, r) {
			return
		}
		h(w, r)
	})
}

// handleEmployee registers a route that acts on the employee in {id}. A guest
// may use it only when the owner shared the employee's profile with them.
func (s *Server) handleEmployee(pattern string, h http.HandlerFunc) {
	s.handle(pattern, func(w http.ResponseWriter, r *http.Request) {
		if !s.requireEmployee(w, r, r.PathValue("id")) {
			return
		}
		h(w, r)
	})
}

func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request) bool {
	if principalOf(r).Role == store.RoleOwner {
		return true
	}
	writeError(w, http.StatusForbidden, "owner_only", "only the owner of this daemon may do this")
	return false
}

// requireProfile lets the owner through, and a guest only for a profile shared
// with them.
func (s *Server) requireProfile(w http.ResponseWriter, r *http.Request, profileID string) bool {
	p := principalOf(r)
	if p.Role == store.RoleOwner {
		return true
	}
	if p.Role == store.RoleGuest && s.cfg.Store != nil {
		ok, err := s.cfg.Store.ProfileShared(r.Context(), profileID, p.Caller.Login)
		if err != nil {
			s.fail(w, err)
			return false
		}
		if ok {
			return true
		}
	}
	s.refuse(r, p.Caller, "profile "+profileID+" is not shared")
	writeError(w, http.StatusForbidden, "profile_not_shared", "the owner has not shared this subscription profile with you")
	return false
}

func (s *Server) requireEmployee(w http.ResponseWriter, r *http.Request, employeeID string) bool {
	if principalOf(r).Role == store.RoleOwner {
		return true
	}
	e, err := s.cfg.Manager.Service().Employee(r.Context(), employeeID)
	if err != nil {
		s.fail(w, err)
		return false
	}
	return s.requireProfile(w, r, e.ProfileID)
}

// ---- who am I, and the allow-list ----

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	role := "guest"
	if p.Role == store.RoleOwner {
		role = "owner"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"method": string(p.Caller.Method), "login": p.Caller.Login, "device": p.Caller.Device, "role": role,
	})
}

type accessEntryDTO struct {
	Login    string   `json:"login"`
	AddedAt  string   `json:"added_at"`
	AddedBy  string   `json:"added_by"`
	Profiles []string `json:"profiles"` // profile IDs shared with them
}

func (s *Server) listAccess(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "remote access is not set up")
		return
	}
	owner, err := s.cfg.Store.OwnerLogin(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	entries, err := s.cfg.Store.AccessList(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	profiles, err := s.cfg.Manager.Service().Profiles(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]accessEntryDTO, 0, len(entries))
	for _, e := range entries {
		dto := accessEntryDTO{Login: e.Login, AddedAt: e.AddedAt.UTC().Format(time.RFC3339), AddedBy: e.AddedBy, Profiles: []string{}}
		for _, p := range profiles {
			if ok, _ := s.cfg.Store.ProfileShared(r.Context(), p.ID, e.Login); ok {
				dto.Profiles = append(dto.Profiles, p.ID)
			}
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"owner": owner, "entries": out, "sharing_notice": SharingNotice("<login>")})
}

func (s *Server) addAccess(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "remote access is not set up")
		return
	}
	var in struct {
		Login        string `json:"login"`
		Acknowledged bool   `json:"acknowledged"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	login := strings.TrimSpace(in.Login)
	if login == "" {
		writeError(w, http.StatusBadRequest, "invalid", "the login is empty")
		return
	}
	if !in.Acknowledged {
		writeError(w, http.StatusBadRequest, "acknowledgement_required", "adding another person needs acknowledged=true after reading: "+SharingNotice(login))
		return
	}
	err := s.cfg.Store.AddAccess(r.Context(), login, principalOf(r).Caller.String(), SharingNotice(strings.ToLower(login)), time.Now())
	switch {
	case errors.Is(err, store.ErrIsOwner):
		writeError(w, http.StatusConflict, "is_owner", "that login is the owner and is always allowed")
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	_, _ = s.cfg.Store.AppendAudit(r.Context(), store.AuditRow{Kind: "access", Action: "person added", Detail: strings.ToLower(login), Decision: "allowed"})
	noContent(w)
}

func (s *Server) removeAccess(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "remote access is not set up")
		return
	}
	login := r.PathValue("login")
	if err := s.cfg.Store.RemoveAccess(r.Context(), login); err != nil {
		s.fail(w, err)
		return
	}
	_, _ = s.cfg.Store.AppendAudit(r.Context(), store.AuditRow{Kind: "access", Action: "person removed", Detail: strings.ToLower(login), Decision: "denied"})
	noContent(w)
}

func (s *Server) shareProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "remote access is not set up")
		return
	}
	var in struct {
		Login string `json:"login"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, err := s.cfg.Manager.Service().Profile(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	role, err := s.cfg.Store.RoleOf(r.Context(), in.Login)
	if err != nil {
		s.fail(w, err)
		return
	}
	if role != store.RoleGuest {
		writeError(w, http.StatusBadRequest, "not_on_list", "add the person to the allow-list first")
		return
	}
	if err := s.cfg.Store.ShareProfile(r.Context(), r.PathValue("id"), in.Login, principalOf(r).Caller.String(), time.Now()); err != nil {
		s.fail(w, err)
		return
	}
	_, _ = s.cfg.Store.AppendAudit(r.Context(), store.AuditRow{Kind: "access", Action: "profile shared", Detail: r.PathValue("id") + " with " + strings.ToLower(in.Login), Decision: "allowed"})
	noContent(w)
}

func (s *Server) unshareProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "remote access is not set up")
		return
	}
	if err := s.cfg.Store.UnshareProfile(r.Context(), r.PathValue("id"), r.PathValue("login")); err != nil {
		s.fail(w, err)
		return
	}
	_, _ = s.cfg.Store.AppendAudit(r.Context(), store.AuditRow{Kind: "access", Action: "profile unshared", Detail: r.PathValue("id") + " from " + strings.ToLower(r.PathValue("login")), Decision: "denied"})
	noContent(w)
}
