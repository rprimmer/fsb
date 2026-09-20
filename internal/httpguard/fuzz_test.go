package httpguard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The oracle below is written from the security requirements, not from the
// middleware: a request may reach the application only if it is a GET/HEAD to
// one of our own hostnames, carries no foreign or oddly-formed Origin/Referer,
// is not marked cross-site by the browser, and presents the session cookie.
var (
	strictOrigin  = regexp.MustCompile(`(?i)^http://(127\.0\.0\.1|localhost):4242$`)
	strictReferer = regexp.MustCompile(`(?i)^http://(127\.0\.0\.1|localhost):4242(/[^@\\]*)?$`)
)

func FuzzMiddlewareOnlyAdmitsLegitimateRequests(f *testing.F) {
	f.Add("GET", "127.0.0.1:4242", "", "", "", "SESSION", "/x")
	f.Add("GET", "localhost:4242", "http://localhost:4242", "http://127.0.0.1:4242/a", "same-origin", "SESSION", "/api/list")
	f.Add("HEAD", "LOCALHOST:4242", "", "", "none", "SESSION", "/")
	f.Add("GET", "evil.example:4242", "", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "http://evil.example", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "null", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "http://user@localhost:4242", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "http://localhost:4242?x=1", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "http://localhost:4242#frag", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "http://localhost:4242@evil.example/", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "http://localhost:4242\\@evil.example/", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "", "cross-site", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "", "same-site", "SESSION", "/x")
	f.Add("POST", "127.0.0.1:4242", "", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "", "", "", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "", "", "wrong", "/x")
	f.Add("GET", "127.0.0.1:4242", "", "", "", "SESSION", "/?token=abc")
	f.Add("get", "127.0.0.1:4242", "", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242 ", "", "", "", "SESSION", "/x")
	f.Add("GET", "127.0.0.1:4242.", "", "", "", "SESSION", "/x")
	f.Add("GET", "[::1]:4242", "", "", "", "SESSION", "/x")

	f.Fuzz(func(t *testing.T, method, host, origin, referer, fetchSite, cookie, path string) {
		a, err := NewAuth()
		if err != nil {
			t.Fatal(err)
		}
		reached := false
		h := Middleware(a, 4242)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

		if cookie == "SESSION" {
			cookie = a.session // let the fuzzer stand for a legitimate cookie
		}
		req := &http.Request{Method: method, Host: host, Header: http.Header{}, URL: &url.URL{Path: path}, RequestURI: path}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if !reached {
			return
		}

		// The request reached the application: every requirement must hold.
		if method != http.MethodGet && method != http.MethodHead {
			t.Fatalf("method %q reached the application", method)
		}
		if lh := strings.ToLower(host); lh != "127.0.0.1:4242" && lh != "localhost:4242" {
			t.Fatalf("Host %q reached the application", host)
		}
		if origin != "" && !strictOrigin.MatchString(origin) {
			t.Fatalf("Origin %q reached the application", origin)
		}
		if referer != "" && !strictReferer.MatchString(referer) {
			t.Fatalf("Referer %q reached the application", referer)
		}
		switch fetchSite {
		case "", "same-origin", "none":
		default:
			t.Fatalf("Sec-Fetch-Site %q reached the application", fetchSite)
		}
		if cookie != a.session {
			t.Fatalf("cookie %q reached the application without being the session", cookie)
		}
	})
}

// A launch token is single-use and only ever valid at "/".
func FuzzLaunchTokenCannotBeReplayedOrMisplaced(f *testing.F) {
	f.Add("/", "TOKEN")
	f.Add("/api/list", "TOKEN")
	f.Add("//", "TOKEN")
	f.Add("/?", "TOKEN")
	f.Add("/", "")
	f.Fuzz(func(t *testing.T, path, token string) {
		a, err := NewAuth()
		if err != nil {
			t.Fatal(err)
		}
		if token == "TOKEN" {
			token = a.LaunchToken()
		}
		h := Middleware(a, 4242)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		try := func() *httptest.ResponseRecorder {
			q := url.Values{"token": {token}}
			req := &http.Request{Method: "GET", Host: "127.0.0.1:4242", Header: http.Header{}, URL: &url.URL{Path: path, RawQuery: q.Encode()}, RequestURI: path}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		}
		first := try()
		second := try()
		if second.Code == http.StatusSeeOther {
			t.Fatalf("the launch token worked twice (path %q)", path)
		}
		if first.Code == http.StatusSeeOther && path != "/" {
			t.Fatalf("the launch token was accepted at %q, not only at /", path)
		}
		if first.Code == http.StatusSeeOther && token != a.LaunchToken() {
			t.Fatalf("a wrong token %q was accepted", token)
		}
	})
}
