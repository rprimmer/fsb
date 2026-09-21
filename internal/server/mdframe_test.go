package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMarkdownFrameIsServedWithItsOwnStrictPolicy(t *testing.T) {
	e := newEnv(t, false, nil)
	resp, err := e.client.Get(e.base + "/api/mdframe")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `id="md"`) {
		t.Fatalf("status=%d body=%.80q", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN so fsb's own page can frame it", got)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	// The rest of the app keeps refusing to be framed.
	r2, _ := e.client.Get(e.base + "/")
	r2.Body.Close()
	if r2.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(r2.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("the app shell must still forbid framing: %v", r2.Header)
	}
}

func TestMarkdownFrameRequiresASession(t *testing.T) {
	e := newEnv(t, false, nil)
	resp, err := (&http.Client{}).Get(e.base + "/api/mdframe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("unauthenticated status = %d, want 403", resp.StatusCode)
	}
}
