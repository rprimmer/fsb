package httpguard

import (
	"errors"
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
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return a, srv
}

func launch(t *testing.T, a *Auth, srv *httptest.Server) int {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/" + a.Prefix() + "/?token=" + a.LaunchToken())
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
	resp, err := http.Get(srv.URL + "/" + a.Prefix() + "/?token=wrong")
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
