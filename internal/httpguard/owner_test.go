package httpguard

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// launchServer serves Middleware over a real loopback connection.
func launchServer(t *testing.T) (*Auth, *httptest.Server) {
	t.Helper()
	a, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	a.RequireOwner(1000, ownerIs(t, 1000))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(Middleware(a, ln.Addr().(*net.TCPAddr).Port)(okHandler))
	srv.Config.ConnContext = ConnContext
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return a, srv
}

func launch(t *testing.T, a *Auth, srv *httptest.Server) int {
	t.Helper()
	// A new connection each time: the owner is looked up once per connection.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/?token=" + a.LaunchToken())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Another user who read the launch URL (from a command line) and uses it
// first is refused, and the token stays unused, so the person's own browser
// still gets in afterwards. fsb warns, without repeating the token.
func TestLaunchFromAnotherUsersConnectionIsRefusedAndTheTokenKept(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	var connUID atomic.Int64
	connUID.Store(1001) // the first connection belongs to another user
	a.RequireOwner(1000, func(server, client netip.AddrPort) (int, error) {
		ownerIs(t, 0)(server, client)
		return int(connUID.Load()), nil
	})
	if code := launch(t, a, srv); code != http.StatusForbidden {
		t.Fatalf("launch from another user = %d, want 403", code)
	}
	if got := w.all(); len(got) != 1 || !strings.Contains(got[0], "another user") || strings.Contains(got[0], a.LaunchToken()) {
		t.Errorf("warning = %q; want one naming another user, without the token", got)
	}
	connUID.Store(1000)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("the person's own launch afterwards = %d, want 303 (the token must not be used up)", code)
	}
}

// When the owner cannot be found, the token is refused (fail closed) and kept.
func TestLaunchWhoseOwnerIsUnknownIsRefusedAndTheTokenKept(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	var known atomic.Bool
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) {
		if !known.Load() {
			return 0, errors.New("not found")
		}
		return 1000, nil
	})
	if code := launch(t, a, srv); code != http.StatusForbidden {
		t.Fatalf("launch with an unknown owner = %d, want 403", code)
	}
	if got := w.all(); len(got) != 1 {
		t.Errorf("warning = %q; want one", got)
	}
	known.Store(true)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("launch afterwards = %d, want 303", code)
	}
}

// A wrong token is refused without looking up the connection at all.
func TestAWrongTokenIsRefusedWithoutALookup(t *testing.T) {
	a, srv := launchServer(t)
	var looked atomic.Bool
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { looked.Store(true); return 1000, nil })
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Get(srv.URL + "/?token=wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || looked.Load() {
		t.Errorf("wrong token: status %d, looked up %v; want 403 without a lookup", resp.StatusCode, looked.Load())
	}
}

// ownerIs is a fake connowner.Lookup that reports uid for the connection the
// request really arrived on, and checks that it is given that connection: the
// server's address and the client's, which differ only in port here.
func ownerIs(t *testing.T, uid int) func(server, client netip.AddrPort) (int, error) {
	return func(server, client netip.AddrPort) (int, error) {
		if !server.Addr().IsLoopback() || !client.Addr().IsLoopback() || server.Port() == client.Port() || client.Port() == 0 {
			t.Errorf("owner asked about server %v, client %v", server, client)
		}
		return uid, nil
	}
}

// Without an owner check (a platform that cannot tell) the launch works as before.
func TestLaunchWithoutAnOwnerCheck(t *testing.T) {
	a, srv := launchServer(t)
	a.RequireOwner(1000, nil)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("launch = %d, want 303", code)
	}
}

// warnings collects warnings, which arrive on the server's goroutine.
type warnings struct {
	mu   sync.Mutex
	msgs []string
}

func (w *warnings) add(s string) { w.mu.Lock(); w.msgs = append(w.msgs, s); w.mu.Unlock() }

func (w *warnings) all() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.msgs...)
}

// Once the person's browser has used the token, replaying it is refused like a
// wrong token: no lookup (reading the connection table costs time) and no
// warning (another user could fill the log).
func TestAUsedTokenIsRefusedWithoutALookupOrWarning(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	var lookups atomic.Int32
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { lookups.Add(1); return 1001, nil })
	a.used.Store(true) // the person's browser has been in
	for range 5 {
		if code := launch(t, a, srv); code != http.StatusForbidden {
			t.Fatalf("replayed token = %d, want 403", code)
		}
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("%d lookups for a used token, want none", n)
	}
	if got := w.all(); len(got) != 0 {
		t.Errorf("warnings for a used token: %q", got)
	}
}

// Refusals before the token is used are reported, but only the first few.
func TestRefusalWarningsAreLimited(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { return 1001, nil })
	for range 10 {
		launch(t, a, srv)
	}
	got := w.all()
	if len(got) != maxRefusalWarnings+1 || !strings.Contains(got[len(got)-1], "not reported") {
		t.Errorf("warnings after 10 refusals = %q; want %d, then one saying the rest are not reported", got, maxRefusalWarnings)
	}
}

// fsb run as root (sudo fsb) refuses the person's own browser; the warning
// says so instead of blaming another user.
func TestRootFsbSaysSoInsteadOfBlamingAnotherUser(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	a.RequireOwner(0, func(netip.AddrPort, netip.AddrPort) (int, error) { return 1000, nil })
	if code := launch(t, a, srv); code != http.StatusForbidden {
		t.Fatalf("launch = %d, want 403", code)
	}
	if got := w.all(); len(got) != 1 || !strings.Contains(got[0], "runs as root") || strings.Contains(got[0], "another user") {
		t.Errorf("warning = %q", got)
	}
}

// session sends a request under the prefix with the session cookie, as a
// server that captured the cookie from the user's browser would replay it.
func session(t *testing.T, a *Auth, srv *httptest.Server, cookie string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/"+a.Prefix()+"/api/status", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The session cookie is sent by the user's browser to any server on
// 127.0.0.1 whose path starts with the prefix, so another user who learns the
// prefix can capture it with a page of their own and replay it. A request
// with the session therefore also has to come from fsb's own user; fsb warns,
// once per cause, that the cookie may have been taken.
func TestASessionFromAnotherUsersConnectionIsRefused(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	var connUID atomic.Int64
	connUID.Store(1000)
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { return int(connUID.Load()), nil })
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("launch = %d", code)
	}
	if code := session(t, a, srv, a.session); code != http.StatusOK {
		t.Fatalf("the user's own session = %d, want 200", code)
	}
	connUID.Store(1001)
	if code := session(t, a, srv, a.session); code != http.StatusForbidden {
		t.Fatalf("the session from another user's connection = %d, want 403", code)
	}
	if got := w.all(); len(got) != 1 || !strings.Contains(got[0], "another user") || strings.Contains(got[0], a.session) {
		t.Errorf("warning = %q; want one naming another user, without the cookie", got)
	}
	connUID.Store(1000)
	if code := session(t, a, srv, a.session); code != http.StatusOK {
		t.Fatalf("the user's own session afterwards = %d, want 200", code)
	}
}

// A request without the session is refused without a lookup: only the
// holder of the cookie can make fsb read the connection table.
func TestNoLookupWithoutTheSession(t *testing.T) {
	a, srv := launchServer(t)
	var lookups atomic.Int32
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { lookups.Add(1); return 1001, nil })
	for _, c := range []string{"", "wrong"} {
		if code := session(t, a, srv, c); code != http.StatusForbidden {
			t.Errorf("cookie %q: %d, want 403", c, code)
		}
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("%d lookups for requests without the session, want none", n)
	}
}

// The owner is looked up once per connection, not once per request: a page
// makes many requests over a few kept-alive connections.
func TestTheOwnerIsLookedUpOncePerConnection(t *testing.T) {
	a, srv := launchServer(t)
	var lookups atomic.Int32
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { lookups.Add(1); return 1000, nil })
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1}}
	for range 5 {
		req, _ := http.NewRequest("GET", srv.URL+"/"+a.Prefix()+"/api/status", nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: a.session})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	if n := lookups.Load(); n != 1 {
		t.Errorf("%d lookups for five requests on one connection, want 1", n)
	}
}

// The warning that the session cookie may have been captured is not used up
// by refused launches: another user trying the token first must not be able
// to silence it.
func TestCookieWarningHasItsOwnBudget(t *testing.T) {
	a, srv := launchServer(t)
	w := &warnings{}
	a.SetWarn(w.add)
	var connUID atomic.Int64
	connUID.Store(1001)
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) { return int(connUID.Load()), nil })
	for range maxRefusalWarnings + 2 {
		launch(t, a, srv) // another user's attempts fill the launch warnings
	}
	connUID.Store(1000)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("own launch = %d", code)
	}
	connUID.Store(1001)
	if code := session(t, a, srv, a.session); code != http.StatusForbidden {
		t.Fatalf("replayed cookie = %d, want 403", code)
	}
	got := w.all()
	if last := got[len(got)-1]; !strings.Contains(last, "captured") {
		t.Errorf("no warning that the cookie may have been captured; warnings: %q", got)
	}
}

// A failed lookup is not remembered for the connection: the next request on
// it looks again, so one bad moment does not refuse a whole connection.
func TestAFailedLookupIsNotRememberedForTheConnection(t *testing.T) {
	a, srv := launchServer(t)
	var calls atomic.Int32
	a.RequireOwner(1000, func(netip.AddrPort, netip.AddrPort) (int, error) {
		if calls.Add(1) == 1 {
			return 0, errors.New("not listed yet")
		}
		return 1000, nil
	})
	a.used.Store(true) // the browser has its session
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1}}
	var codes []int
	for range 3 {
		req, _ := http.NewRequest("GET", srv.URL+"/"+a.Prefix()+"/api/status", nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: a.session})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if codes[0] != http.StatusForbidden || codes[1] != http.StatusOK || codes[2] != http.StatusOK {
		t.Errorf("statuses on one connection = %v; want 403, then 200 once the owner is found", codes)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("%d lookups; want 2 (the failure is not remembered, the success is)", n)
	}
}
