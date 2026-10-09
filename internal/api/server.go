// Package api exposes the daemon over HTTP and WebSocket on loopback. It is
// the only contract between the daemon and its clients (docs/API.md).
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"aotus/internal/orchestrator"
	"aotus/internal/permissions"
)

// Config is what the server needs.
type Config struct {
	Manager *orchestrator.Manager
	Broker  *permissions.Broker
	// Token is the local API token; every request must carry it.
	Token   string
	Version string
	// Logger receives server-side details of failures; nil means slog.Default.
	Logger *slog.Logger
	// AllowedOrigins lists the browser origins that may call the API. The
	// default is none: the daemon's clients are programs, which send no Origin
	// header, and a web page must never be able to talk to the daemon.
	AllowedOrigins []string
}

// Server is the API's HTTP handler.
type Server struct {
	cfg    Config
	log    *slog.Logger
	mux    *http.ServeMux
	routes []string
}

// New builds the API handler. Everything goes through the same guards, in this
// order: the Host header must name this computer (against DNS rebinding), a
// request carrying an Origin header is refused unless allow-listed (against
// web pages), and the bearer token must be valid. No response ever carries
// CORS headers.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, log: cfg.Logger, mux: http.NewServeMux()}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.register()
	return s
}

// Routes lists the route patterns, for tests that check every one is guarded.
func (s *Server) Routes() []string { return append([]string(nil), s.routes...) }

// handle registers a route behind the guards.
func (s *Server) handle(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, pattern)
	s.mux.HandleFunc(pattern, h)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Responses are for the program that asked, never cacheable or embeddable.
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")

	if !localHost(r.Host) {
		writeError(w, http.StatusForbidden, "bad_host", "this address is not served: use 127.0.0.1, localhost or [::1]")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		writeError(w, http.StatusForbidden, "bad_origin", "requests from web pages are not accepted")
		return
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
		h.Set("WWW-Authenticate", `Bearer realm="aotus"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", "a valid token is required")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.cfg.AllowedOrigins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// localHost reports whether a Host header names this computer's loopback.
func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback() && ip.Zone() == ""
}

// maxBody bounds every JSON request body.
const maxBody = 1 << 20

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "bad_json", "the request body is not valid: "+err.Error())
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, message
	writeJSON(w, status, b)
}

func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// parseTime reads an RFC 3339 time; empty means the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
