package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/guard"
)

// The mapping from guard.ErrDataless to a 409 with a helpful body had never
// been exercised by any test: the real File Provider flag that produces it
// cannot be set from an unprivileged test (see internal/guard/dataless_darwin_test.go),
// so every existing dataless test stopped at guard.isDataless itself. This
// tests the one function (fail) that every endpoint funnels errors through,
// which needs no real cloud-only file: it is a pure mapping from an error
// value to a status and a body.
func TestFailMapsDatalessToConflictWithAHelpfulBody(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.fail(rec, "/whatever", guard.ErrDataless)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cloud") || !strings.Contains(body, "not downloaded") {
		t.Errorf("body should explain that this is stored in the cloud and not downloaded, got %q", body)
	}
}
