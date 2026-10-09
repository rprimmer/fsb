// Package httpguard holds the middleware that makes fsb safe to run as a
// localhost web server: Host/Origin validation (DNS rebinding and cross-site
// requests), a single-use launch token exchanged for an HttpOnly session
// cookie, read-only method enforcement, and security headers.
package httpguard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// CookieName is the session cookie set after the launch token is exchanged.
const CookieName = "fsb_session"

// Auth holds the per-launch secrets. The launch token appears once, in the URL
// printed at startup, and can be exchanged exactly once for the session cookie,
// so a copy left in shell or browser history is useless.
//
// Everything is served under a random per-launch path prefix, "/<prefix>/", and
// the session cookie is set with that path. Browsers scope cookies by host and
// never by port, so without this the cookie would be sent to every other web
// server on 127.0.0.1 that the same browser visits; with it, the browser sends
// the cookie only for URLs under the prefix, which no other server has.
//
// Where the platform can tell who owns a connection (Linux), the launch token
// is accepted only from a connection of fsb's own user: the launch URL is in
// the command line of the program that opens the browser, which other users
// can read. Refusing a connection does not use the token up, so another user
// who reads it and uses it first cannot keep the person's browser out.
type Auth struct {
	launch  string
	session string
	prefix  string
	used    atomic.Bool

	uid   int                                              // fsb's own user
	owner func(server, client netip.AddrPort) (int, error) // nil: no check
	warn  func(string)

	refusals atomic.Int32
}

// RequireOwner makes the launch exchange accept only connections whose client
// end belongs to user uid, as owner reports (connowner.Lookup); a nil owner,
// where the platform cannot tell, leaves the check off.
func (a *Auth) RequireOwner(uid int, owner func(server, client netip.AddrPort) (int, error)) {
	a.uid, a.owner = uid, owner
}

// SetWarn sets where warnings go (a refused launch); by default, nowhere.
func (a *Auth) SetWarn(f func(string)) { a.warn = f }

// NewAuth generates fresh 256-bit random secrets.
func NewAuth() (*Auth, error) {
	l, err := randToken()
	if err != nil {
		return nil, err
	}
	s, err := randToken()
	if err != nil {
		return nil, err
	}
	pb := make([]byte, 16)
	if _, err := rand.Read(pb); err != nil {
		return nil, fmt.Errorf("generating path prefix: %w", err)
	}
	return &Auth{launch: l, session: s, prefix: base64.RawURLEncoding.EncodeToString(pb)}, nil
}

// Prefix returns the random path prefix, without slashes. The application is
// served at "/<prefix>/"; the launch URL is "/?token=...", which names no prefix.
func (a *Auth) Prefix() string { return a.prefix }

// LaunchToken returns the single-use token for the launch URL.
func (a *Auth) LaunchToken() string { return a.launch }

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Middleware enforces the localhost security policy for a server on port.
func Middleware(a *Auth, port int) func(http.Handler) http.Handler {
	hosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setSecurityHeaders(w)

			// DNS rebinding: a page on evil.example resolving to 127.0.0.1
			// still sends its own hostname in Host.
			if !hosts[strings.ToLower(r.Host)] {
				forbid(w)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !sameOrigin(r, hosts) {
				forbid(w)
				return
			}

			// The launch URL is the bare root: it names no prefix, so the command
			// line of the program that opens the browser, which other users can
			// read, does not reveal the path the session cookie is sent for.
			if r.URL.Path == "/" && r.URL.Query().Has("token") {
				exchange(w, r, a)
				return
			}

			// Only URLs under the prefix exist; anything else is refused exactly
			// like an unauthenticated request.
			rest, ok := strings.CutPrefix(r.URL.Path, "/"+a.prefix+"/")
			if !ok {
				forbid(w)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + rest
			r2.URL.RawPath = ""

			c, err := r.Cookie(CookieName)
			if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.session)) != 1 {
				forbid(w)
				return
			}
			// The browser sends the cookie to any server on 127.0.0.1 whose path
			// starts with the prefix, so a server of another user that learned
			// the prefix could capture it. With the session, the connection must
			// still be fsb's own user's (looked up only now, for the holder of
			// the cookie, and once per connection).
			if a.owner != nil {
				if why := a.notOwn(r, false); why != "" {
					a.warnRefused("refused a request: " + why)
					forbid(w)
					return
				}
			}
			next.ServeHTTP(w, r2)
		})
	}
}

func exchange(w http.ResponseWriter, r *http.Request, a *Auth) {
	tok := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(a.launch)) != 1 {
		forbid(w)
		return
	}
	// A used token is refused like a wrong one: no lookup, no warning, so
	// replaying it costs nothing and fills no log.
	if a.used.Load() {
		forbid(w)
		return
	}
	// Checked before the token is used, so that a refused connection leaves it
	// for the person's own browser.
	if a.owner != nil {
		if why := a.notOwn(r, true); why != "" {
			a.warnRefused("refused the launch URL: " + why)
			forbid(w)
			return
		}
	}
	if a.used.CompareAndSwap(false, true) {
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    a.session,
			Path:     "/" + a.prefix + "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Location", "/"+a.prefix+"/") // drop the token from the address bar
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	forbid(w)
}

// notOwn says why r's connection cannot be shown to belong to fsb's own user,
// or returns "" when it does. launch says whether r presents the launch token
// (otherwise it carries the session).
func (a *Auth) notOwn(r *http.Request, launch bool) string {
	uid, err := a.connectionOwner(r)
	switch {
	case err != nil && launch:
		return "could not tell which user opened it (" + err.Error() + "); on this system the launch URL cannot be used"
	case err != nil:
		return "could not tell which user sent a request with your session (" + err.Error() + ")"
	case uid == a.uid:
		return ""
	case a.uid == 0:
		return fmt.Sprintf("fsb runs as root, and the connection belongs to uid %d; start fsb as your own user", uid)
	case launch:
		return fmt.Sprintf("it was opened by another user (uid %d), who may have read it from a command line; your own browser can still use it", uid)
	}
	return fmt.Sprintf("a request with your session came from another user (uid %d): your session cookie may have been captured; restart fsb to start a new session", uid)
}

// connOwner remembers, for one connection, who owns its client end.
type connOwner struct {
	once sync.Once
	uid  int
	err  error
}

type connKey struct{}

// ConnContext gives each connection a place to remember its owner, so that it
// is looked up once per connection rather than once per request. Set it as
// the http.Server's ConnContext.
func ConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connOwner{})
}

// connectionOwner returns the owner of r's connection, from the connection's
// memory when the server set ConnContext. It is the owner, not a verdict,
// that is remembered.
func (a *Auth) connectionOwner(r *http.Request) (int, error) {
	if c, ok := r.Context().Value(connKey{}).(*connOwner); ok {
		c.once.Do(func() { c.uid, c.err = a.lookupOwner(r) })
		return c.uid, c.err
	}
	return a.lookupOwner(r)
}

func (a *Auth) lookupOwner(r *http.Request) (int, error) {
	local, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if local == nil {
		return 0, errors.New("its connection is unknown")
	}
	server, err1 := netip.ParseAddrPort(local.String())
	client, err2 := netip.ParseAddrPort(r.RemoteAddr)
	if err1 != nil || err2 != nil {
		return 0, errors.New("its connection is unknown")
	}
	server = netip.AddrPortFrom(server.Addr().Unmap(), server.Port())
	client = netip.AddrPortFrom(client.Addr().Unmap(), client.Port())
	return a.owner(server, client)
}

// maxRefusalWarnings is how many refused launches are reported; another
// user could otherwise fill the terminal or background.log with them.
const maxRefusalWarnings = 3

func (a *Auth) warnRefused(msg string) {
	if a.warn == nil {
		return
	}
	switch n := a.refusals.Add(1); {
	case n <= maxRefusalWarnings:
		a.warn(msg)
	case n == maxRefusalWarnings+1:
		a.warn("refused another connection of another user; further refusals are not reported")
	}
}

// sameOrigin rejects requests that a cross-site page could have triggered.
func sameOrigin(r *http.Request, hosts map[string]bool) bool {
	// A browser serializes Origin as exactly scheme://host[:port]. Anything
	// else (userinfo, path, query, fragment, an opaque form) is not something a
	// browser sends, so it is refused rather than interpreted.
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Scheme != "http" || !hosts[strings.ToLower(u.Host)] ||
			u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return false // includes the opaque "null" origin
		}
	}
	// Referer may carry a path and query, but never credentials.
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil || u.Scheme != "http" || !hosts[strings.ToLower(u.Host)] || u.User != nil || u.Opaque != "" {
			return false
		}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

func forbid(w http.ResponseWriter) { http.Error(w, "forbidden", http.StatusForbidden) }

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// AccessLog logs method, path and status. It deliberately never logs the query
// string, which is where the launch token travels.
func AccessLog(l *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			l.Printf("%s %s %d", r.Method, r.URL.Path, rec.status)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController reach the underlying writer (Flush).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
