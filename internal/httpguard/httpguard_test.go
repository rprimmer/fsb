package httpguard

import (
	"bytes"
	"encoding/base64"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const port = 4242

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
})

type harness struct {
	auth   *Auth
	h      http.Handler
	cookie *http.Cookie
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	a, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	return &harness{auth: a, h: Middleware(a, port)(okHandler)}
}

// login performs the launch-token exchange and stores the session cookie.
func (hn *harness) login(t *testing.T) {
	t.Helper()
	rec := hn.do("GET", "/?token="+hn.auth.LaunchToken(), nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("exchange status = %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			hn.cookie = c
		}
	}
	if hn.cookie == nil {
		t.Fatal("no session cookie set")
	}
}

func (hn *harness) do(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "127.0.0.1:4242"
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	if hn.cookie != nil && hdr["Cookie"] == "" {
		r.AddCookie(hn.cookie)
	}
	rec := httptest.NewRecorder()
	hn.h.ServeHTTP(rec, r)
	return rec
}

func TestTokenEntropyAndUniqueness(t *testing.T) {
	a, _ := NewAuth()
	b, _ := NewAuth()
	if a.LaunchToken() == b.LaunchToken() || a.session == b.session {
		t.Fatal("tokens must differ between launches")
	}
	raw, err := base64.RawURLEncoding.DecodeString(a.LaunchToken())
	if err != nil || len(raw) < 16 {
		t.Fatalf("launch token must carry at least 128 bits, got %d bytes (%v)", len(raw), err)
	}
	if a.LaunchToken() == a.session {
		t.Fatal("launch token and session token must be distinct")
	}
}

func TestExchangeSetsHardenedCookieAndRedirects(t *testing.T) {
	hn := newHarness(t)
	rec := hn.do("GET", "/?token="+hn.auth.LaunchToken(), nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	c := rec.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie not hardened: %+v", c)
	}
	if c.Value == hn.auth.LaunchToken() {
		t.Fatal("the cookie must not reuse the launch token")
	}
}

func TestLaunchTokenIsSingleUse(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	hn.cookie = nil
	if rec := hn.do("GET", "/?token="+hn.auth.LaunchToken(), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reused launch token: status = %d, want 403", rec.Code)
	}
}

func TestWrongLaunchTokenRejected(t *testing.T) {
	hn := newHarness(t)
	for _, tok := range []string{"", "wrong", hn.auth.LaunchToken() + "x", strings.ToUpper(hn.auth.LaunchToken())} {
		if rec := hn.do("GET", "/?token="+tok, nil); rec.Code != http.StatusForbidden {
			t.Errorf("token %q: status = %d, want 403", tok, rec.Code)
		}
	}
	// The failed attempts must not have burned the real token.
	hn.login(t)
}

func TestLaunchTokenOnlyWorksAtRoot(t *testing.T) {
	hn := newHarness(t)
	if rec := hn.do("GET", "/api/list?token="+hn.auth.LaunchToken(), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequiresSessionCookie(t *testing.T) {
	hn := newHarness(t)
	if rec := hn.do("GET", "/anything", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no cookie: status = %d", rec.Code)
	}
	hn.cookie = &http.Cookie{Name: CookieName, Value: "guess"}
	if rec := hn.do("GET", "/anything", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong cookie: status = %d", rec.Code)
	}
	hn.cookie = nil
	hn.login(t)
	if rec := hn.do("GET", "/anything", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid cookie: status = %d", rec.Code)
	}
	// The launch token is not a valid session credential.
	hn.cookie = &http.Cookie{Name: CookieName, Value: hn.auth.LaunchToken()}
	if rec := hn.do("GET", "/anything", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("launch token as cookie: status = %d", rec.Code)
	}
}

func TestHostValidation(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	cases := map[string]int{
		"127.0.0.1:4242":              200,
		"localhost:4242":              200,
		"LOCALHOST:4242":              200,
		"127.0.0.1":                   403, // no port
		"127.0.0.1:4243":              403, // wrong port
		"evil.example":                403,
		"evil.example:4242":           403,
		"127.0.0.1.evil.example:4242": 403,
		"localhost.evil.example:4242": 403,
		"[::1]:4242":                  403,
		"0.0.0.0:4242":                403,
		"":                            403,
	}
	for host, want := range cases {
		rec := hn.do("GET", "/x", map[string]string{"Host": host})
		if rec.Code != want {
			t.Errorf("Host %q: status = %d, want %d", host, rec.Code, want)
		}
	}
}

// A rebinding attacker's browser holds our cookie only if it was set for the
// attacker's origin, but even a valid cookie must not help on a foreign Host.
func TestDNSRebindingWithValidCookieIsRejected(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	rec := hn.do("GET", "/api/file?path=/etc/hosts", map[string]string{"Host": "attacker.example:4242"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestOnlyReadMethodsAllowed(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
		rec := hn.do(m, "/x", nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", m, rec.Code)
		}
	}
	for _, m := range []string{"GET", "HEAD"} {
		if rec := hn.do(m, "/x", nil); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", m, rec.Code)
		}
	}
}

func TestOriginRefererAndFetchMetadata(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	tests := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"same origin", map[string]string{"Origin": "http://127.0.0.1:4242"}, 200},
		{"localhost origin", map[string]string{"Origin": "http://localhost:4242"}, 200},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, 403},
		{"foreign origin same port", map[string]string{"Origin": "http://evil.example:4242"}, 403},
		{"null origin", map[string]string{"Origin": "null"}, 403},
		{"https origin", map[string]string{"Origin": "https://127.0.0.1:4242"}, 403},
		{"other local port", map[string]string{"Origin": "http://127.0.0.1:9999"}, 403},
		{"same referer", map[string]string{"Referer": "http://127.0.0.1:4242/a/b"}, 200},
		{"foreign referer", map[string]string{"Referer": "http://evil.example/page"}, 403},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same-site fetch", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"user navigation", map[string]string{"Sec-Fetch-Site": "none"}, 200},
	}
	for _, tc := range tests {
		if rec := hn.do("GET", "/x", tc.hdr); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestNoCORSHeaders(t *testing.T) {
	hn := newHarness(t)
	hn.login(t)
	rec := hn.do("GET", "/x", map[string]string{"Origin": "http://127.0.0.1:4242"})
	for h := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(h), "access-control-") {
			t.Errorf("unexpected CORS header %s", h)
		}
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	hn := newHarness(t)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"forbidden":  hn.do("GET", "/x", nil),
		"bad host":   hn.do("GET", "/x", map[string]string{"Host": "evil.example"}),
		"bad method": hn.do("POST", "/x", nil),
	} {
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" ||
			!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: missing security headers: %v", name, h)
		}
	}
	hn.login(t)
	if rec := hn.do("GET", "/x", nil); rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("authorized response is missing nosniff")
	}
}

func TestAccessLogNeverContainsTheToken(t *testing.T) {
	var buf bytes.Buffer
	a, _ := NewAuth()
	h := AccessLog(log.New(&buf, "", 0))(Middleware(a, port)(okHandler))

	r := httptest.NewRequest("GET", "/?token="+a.LaunchToken(), nil)
	r.Host = "127.0.0.1:4242"
	h.ServeHTTP(httptest.NewRecorder(), r)

	out := buf.String()
	if out == "" {
		t.Fatal("expected a log line")
	}
	if strings.Contains(out, a.LaunchToken()) || strings.Contains(out, a.session) || strings.Contains(out, "token") {
		t.Fatalf("log leaks the token: %q", out)
	}
}
