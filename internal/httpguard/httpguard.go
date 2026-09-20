// Package httpguard holds the middleware that makes fsb safe to run as a
// localhost web server: Host/Origin validation (DNS rebinding and cross-site
// requests), a single-use launch token exchanged for an HttpOnly session
// cookie, read-only method enforcement, and security headers.
package httpguard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// CookieName is the session cookie set after the launch token is exchanged.
const CookieName = "fsb_session"

// Auth holds the per-launch secrets. The launch token appears once, in the URL
// printed at startup, and can be exchanged exactly once for the session cookie,
// so a copy left in shell or browser history is useless.
type Auth struct {
	launch  string
	session string
	used    atomic.Bool
}

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
	return &Auth{launch: l, session: s}, nil
}

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

			if r.URL.Path == "/" && r.URL.Query().Has("token") {
				exchange(w, r, a)
				return
			}

			c, err := r.Cookie(CookieName)
			if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.session)) != 1 {
				forbid(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func exchange(w http.ResponseWriter, r *http.Request, a *Auth) {
	tok := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(a.launch)) == 1 && a.used.CompareAndSwap(false, true) {
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    a.session,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Location", "/") // drop the token from the address bar
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	forbid(w)
}

// sameOrigin rejects requests that a cross-site page could have triggered.
func sameOrigin(r *http.Request, hosts map[string]bool) bool {
	// A browser serialises Origin as exactly scheme://host[:port]. Anything
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
