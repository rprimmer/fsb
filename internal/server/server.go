// Package server exposes the read-only JSON API and app shell. It must never
// touch the filesystem directly: all access goes through guard.Guard. This is
// enforced by TestHandlersDoNotImportFilesystemPackages.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/httpguard"
	"github.com/rprimmer/fsb/internal/quicklook"
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
	// Home is the user's real home directory, so the UI can expand "~" in the
	// Go to path box.
	Home   string
	Logger *log.Logger
	// Warn receives warnings that are always shown, such as a launch URL or a
	// session cookie refused because another user presented it.
	Warn io.Writer
	// UID and ConnOwner, when ConnOwner is set (connowner.Lookup on Linux),
	// make the launch URL and the session work only on connections of user UID.
	UID       int
	ConnOwner func(server, client netip.AddrPort) (int, error)
	// Thumbnail draws a PNG picture of the document that prepare places in the
	// private folder it is given (quicklook.Thumbnail when nil). Tests
	// substitute their own.
	Thumbnail func(ctx context.Context, size int, prepare func(dir string) (string, error)) ([]byte, error)
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
	if cfg.Thumbnail == nil {
		cfg.Thumbnail = quicklook.Thumbnail
	}
	auth.RequireOwner(cfg.UID, cfg.ConnOwner)
	if cfg.Warn != nil {
		auth.SetWarn(func(msg string) { fmt.Fprintln(cfg.Warn, "fsb: warning: "+msg) })
	}
	return &Server{cfg: cfg, auth: auth}, nil
}

// LaunchToken is the single-use token for the launch URL.
func (s *Server) LaunchToken() string { return s.auth.LaunchToken() }

// LaunchPath is the path of the launch URL, which is this path with "?token="
// and LaunchToken. It is the bare root: the launch URL names no prefix (see
// httpguard), and the browser learns the application's path from the redirect.
func (s *Server) LaunchPath() string { return "/" }

// AppPath is the URL path of the application, "/<random prefix>/".
func (s *Server) AppPath() string { return "/" + s.auth.Prefix() + "/" }

// ConnContext is for http.Server.ConnContext: it lets the owner of each
// connection be looked up once per connection (httpguard.ConnContext).
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	return httpguard.ConnContext(ctx, c)
}

// Handler returns the full handler stack for a server listening on port.
func (s *Server) Handler(port int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/list", s.list)
	mux.HandleFunc("GET /api/file", s.file)
	mux.HandleFunc("GET /api/head", s.head)
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/preview", s.preview)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/mdframe", s.mdframe)
	mux.HandleFunc("GET /api/pdf", s.pdf)
	mux.HandleFunc("GET /api/archive", s.archive)
	mux.HandleFunc("GET /api/quicklook", s.quicklook)
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
		"roots":           wireNames(s.cfg.Guard.Roots()),
		"home":            wireName(s.cfg.Home),
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
		enc.Encode(map[string]any{"path": wireName(p)})
	}
	err := s.cfg.Guard.ListFunc(p, listBatch, func(es []guard.Entry) error {
		start()
		if err := enc.Encode(map[string]any{"entries": wireEntries(es)}); err != nil {
			return err
		}
		rc.Flush()
		return nil
	})
	switch {
	case err == nil:
		start() // an empty directory still gets its header line
		// Without this line the client treats the listing as cut off: a
		// connection that drops between batches must not look complete.
		enc.Encode(map[string]any{"done": true})
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

// Limits for /api/head.
const (
	defaultHeadBytes = 2048
	maxHeadBytes     = 64 * 1024
)

// head returns a classified, bounded look at the start of a file: text is
// returned only when it is valid UTF-8, and binary or cloud-only files report
// their kind and size but never their bytes.
func (s *Server) head(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	n := defaultHeadBytes
	if v := r.URL.Query().Get("bytes"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 || parsed > maxHeadBytes {
			http.Error(w, "bad bytes parameter", http.StatusBadRequest)
			return
		}
		n = parsed
	}
	h, err := s.cfg.Guard.Head(p, n)
	if err != nil {
		s.fail(w, p, err)
		return
	}
	writeJSON(w, h)
}

// meta returns details and extended attributes. values=0 lists attribute
// names only (used by the optional listing column).
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	m, err := s.cfg.Guard.Meta(p, r.URL.Query().Get("values") != "0")
	if err != nil {
		s.fail(w, p, err)
		return
	}
	writeJSON(w, wireMeta(m))
}

// preview serves a raster image inline, and nothing else inline. The type is
// decided from the file's leading bytes by the guard, never from its name, and
// the response is sandboxed so even a polyglot file cannot run anything.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, fi, contentType, err := s.cfg.Guard.OpenImage(p)
	if err != nil {
		s.fail(w, p, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// pdf serves a file inline as a PDF, but only when its leading bytes say it is
// one. It is meant for a frame on fsb's own page, so the browser's built-in
// viewer shows it. PDFs cannot be sandboxed with a CSP sandbox (browsers then
// refuse to show them), so the protection is different from images: the viewer
// runs in the browser's own isolated context, the response can run nothing in
// fsb's origin (script-src 'none', no object or frame content), and it may be
// framed only by fsb's own pages.
func (s *Server) pdf(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, fi, contentType, err := s.cfg.Guard.OpenPDF(p)
	if err != nil {
		s.fail(w, p, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "frame-ancestors 'self'; script-src 'none'")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// thumbSize is the longer side, in pixels, of a Quick Look picture.
const thumbSize = 1200

// quickLookCopyTimeout bounds copying a document for Quick Look (at most
// guard.MaxQuickLookBytes); drawing it has its own limit in package quicklook.
var quickLookCopyTimeout = 20 * time.Second

// quicklook serves the picture macOS Quick Look draws of an iWork or Office
// document. Quick Look is given a private copy that the guard made (see
// guard.CopyForQuickLook), never the user's own path. The picture is served
// like an image preview: a PNG made by fsb's own call, sandboxed.
func (s *Server) quicklook(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	var guardErr error
	png, err := s.cfg.Thumbnail(r.Context(), thumbSize, func(dir string) (string, error) {
		// Copying has its own time limit (drawing has Quick Look's), and both
		// stop when the request is abandoned.
		ctx, cancel := context.WithTimeout(r.Context(), quickLookCopyTimeout)
		defer cancel()
		doc, err := s.cfg.Guard.CopyForQuickLookContext(ctx, p, dir)
		guardErr = err
		return doc, err
	})
	if errors.Is(guardErr, context.DeadlineExceeded) {
		s.logf("quicklook %q: copying took longer than %v", p, quickLookCopyTimeout)
		guardErr = guard.ErrUnsupported // no picture, as for one Quick Look cannot draw
	}
	if guardErr != nil {
		s.fail(w, p, guardErr)
		return
	}
	if err != nil {
		if r.Context().Err() == nil {
			s.logf("quicklook %q: %v", p, err)
		}
		s.fail(w, p, guard.ErrUnsupported)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "no-store")
	w.Write(png)
}

// archive lists the members of a zip, tar or tar.gz file. Nothing is extracted.
func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	l, err := s.cfg.Guard.ListArchiveContext(r.Context(), p)
	if err != nil {
		s.fail(w, p, err)
		return
	}
	writeJSON(w, wireArchive(l))
}

// mdframe serves the fixed page that renders sanitized Markdown inside a
// sandboxed iframe. It is not user content: it is a constant document with its
// own strict policy (see web.MDFrame). It may be framed by fsb's own pages only.
func (s *Server) mdframe(w http.ResponseWriter, r *http.Request) {
	doc, csp := web.MDFrame()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "SAMEORIGIN") // the app's default is DENY
	w.Write(doc)
}

// search streams filename matches as NDJSON: a {"path","query"} line, then
// {"matches":[...]} lines as results arrive, then {"done":true,...}, which says
// whether a limit stopped it (truncated) and whether some folders could not be
// read completely (incomplete).
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("path")
	query := r.URL.Query().Get("q")
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	started := false
	start := func() {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc.Encode(map[string]any{"path": wireName(root), "query": query})
	}

	var pending []guard.SearchMatch
	lastFlush := time.Now()
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		start()
		err := enc.Encode(map[string]any{"matches": wireMatches(pending)})
		pending = pending[:0]
		lastFlush = time.Now()
		rc.Flush()
		return err
	}

	opts := guard.DefaultSearchLimits
	opts.MatchCase = r.URL.Query().Get("case") == "1"
	sum, err := s.cfg.Guard.SearchSummary(r.Context(), root, query, opts, func(m guard.SearchMatch) error {
		pending = append(pending, m)
		if len(pending) >= 25 || time.Since(lastFlush) > 150*time.Millisecond {
			return flush()
		}
		return nil
	})
	switch {
	case err == nil:
		flush()
		start()
		enc.Encode(map[string]any{"done": true, "visited": sum.Visited, "truncated": sum.Truncated, "incomplete": sum.Unreadable > 0})
	case r.Context().Err() != nil:
		// The client went away; nothing to send.
	case !started && len(pending) == 0:
		s.fail(w, root, err)
	default:
		s.logf("search %q interrupted: %v", root, err)
		flush()
		enc.Encode(map[string]any{"error": "search interrupted"})
	}
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
	case errors.Is(err, guard.ErrNotFound), errors.Is(err, guard.ErrNotRegular), errors.Is(err, guard.ErrNotDir), errors.Is(err, guard.ErrIsDir):
		notFound(w)
	case errors.Is(err, guard.ErrDataless):
		http.Error(w, "stored in the cloud and not downloaded; fsb will not trigger a download", http.StatusConflict)
	case errors.Is(err, guard.ErrUnsupported):
		http.Error(w, "unsupported file type", http.StatusUnsupportedMediaType)
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
