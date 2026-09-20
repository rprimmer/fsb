// Package server exposes the read-only JSON API and app shell. It must never
// touch the filesystem directly: all access goes through guard.Guard. This is
// enforced by TestHandlersDoNotImportFilesystemPackages.
package server

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/httpguard"
	"github.com/rprimmer/fsb/web"
)

// listBatch is how many directory entries are read per streamed chunk.
const listBatch = 2000

// Config configures a Server.
type Config struct {
	Guard *guard.Guard
	// Debug makes denied requests answer 404 with the matching rule in the
	// body. Never enable it for anyone but the developer.
	Debug bool
	// CoreDenyMissing lists core deny rules the user has weakened; the UI shows
	// a banner while it is non-empty.
	CoreDenyMissing []string
	Logger          *log.Logger
}

// Server serves the API.
type Server struct {
	cfg  Config
	auth *httpguard.Auth
}

// New creates a Server with fresh per-launch secrets.
func New(cfg Config) (*Server, error) {
	auth, err := httpguard.NewAuth()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, auth: auth}, nil
}

// LaunchToken is the single-use token for the launch URL.
func (s *Server) LaunchToken() string { return s.auth.LaunchToken() }

// Handler returns the full handler stack for a server listening on port.
func (s *Server) Handler(port int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/list", s.list)
	mux.HandleFunc("GET /api/file", s.file)
	mux.Handle("GET /", web.Handler()) // embedded frontend; unknown paths 404

	var h http.Handler = mux
	h = httpguard.Middleware(s.auth, port)(h)
	h = httpguard.AccessLog(s.cfg.Logger)(h)
	return h
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Printf(format, args...)
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	missing := s.cfg.CoreDenyMissing
	if missing == nil {
		missing = []string{}
	}
	writeJSON(w, map[string]any{
		"readOnly":        true,
		"coreDenyMissing": missing,
		"roots":           s.cfg.Guard.Roots(),
	})
}

// list streams a directory as NDJSON so the first screen can render before a
// huge directory has been read: a {"path"} line, then {"entries":[...]} lines
// in directory order (the client sorts), and a final {"error"} line if the
// listing breaks after the response has started.
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	started := false
	start := func() {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc.Encode(map[string]any{"path": p})
	}
	err := s.cfg.Guard.ListFunc(p, listBatch, func(es []guard.Entry) error {
		start()
		if err := enc.Encode(map[string]any{"entries": es}); err != nil {
			return err
		}
		rc.Flush()
		return nil
	})
	switch {
	case err == nil:
		start() // an empty directory still gets its header line
	case !started:
		s.fail(w, p, err)
	default:
		s.logf("listing %q interrupted: %v", p, err)
		enc.Encode(map[string]any{"error": "listing interrupted"})
	}
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, fi, err := s.cfg.Guard.Open(p)
	if err != nil {
		s.fail(w, p, err)
		return
	}
	defer f.Close()
	if fi.IsDir() {
		notFound(w)
		return
	}
	// Untrusted bytes are never rendered from this origin: always a download.
	w.Header().Set("Content-Type", "application/octet-stream")
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// fail maps guard errors to responses. Denied, nonexistent, out-of-root and
// unopenable paths all answer an identical 404 unless Debug is set.
func (s *Server) fail(w http.ResponseWriter, path string, err error) {
	var denied *guard.DeniedError
	switch {
	case errors.As(err, &denied):
		s.logf("denied %q: %s", path, denied.Rule)
		if s.cfg.Debug {
			http.Error(w, "denied by rule: "+denied.Rule, http.StatusNotFound)
			return
		}
		notFound(w)
	case errors.Is(err, guard.ErrNotFound), errors.Is(err, guard.ErrNotRegular), errors.Is(err, guard.ErrNotDir):
		notFound(w)
	case errors.Is(err, guard.ErrPermission):
		http.Error(w, "permission denied", http.StatusForbidden)
	default:
		s.logf("error for %q: %v", path, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func notFound(w http.ResponseWriter) { http.Error(w, "not found", http.StatusNotFound) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
