package httpguard

import (
	"net/http"
	"os"
	"testing"

	"github.com/rprimmer/fsb/internal/connowner"
)

// The twin of the tests with a fake owner: the real lookup accepts this
// process's own connection, and refuses it once fsb's user is someone else.
func TestLaunchWithTheRealOwnerLookup(t *testing.T) {
	if connowner.Lookup == nil {
		t.Fatal("connowner.Lookup must be set on Linux")
	}
	a, srv := launchServer(t)
	a.RequireOwner(os.Geteuid()+1, connowner.Lookup)
	if code := launch(t, a, srv); code != http.StatusForbidden {
		t.Fatalf("launch from a connection of another user = %d, want 403", code)
	}
	a.RequireOwner(os.Geteuid(), connowner.Lookup)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("launch from this user's connection = %d, want 303", code)
	}
}

// The twin for the session: a cookie replayed from a connection of another
// user is refused by the real lookup.
func TestSessionWithTheRealOwnerLookup(t *testing.T) {
	a, srv := launchServer(t)
	a.RequireOwner(os.Geteuid(), connowner.Lookup)
	if code := launch(t, a, srv); code != http.StatusSeeOther {
		t.Fatalf("launch = %d", code)
	}
	if code := session(t, a, srv, a.session); code != http.StatusOK {
		t.Fatalf("own session = %d, want 200", code)
	}
	a.RequireOwner(os.Geteuid()+1, connowner.Lookup)
	if code := session(t, a, srv, a.session); code != http.StatusForbidden {
		t.Fatalf("session from a connection of another user = %d, want 403", code)
	}
}
