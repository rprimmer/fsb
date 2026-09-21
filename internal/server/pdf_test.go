package server

import (
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFIsServedInlineOnlyForRealPDFsAndOnlyToOurOwnFrames(t *testing.T) {
	e := newEnv(t, false, nil)
	pdf := "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"
	write(t, filepath.Join(e.home, "proj", "doc.pdf"), pdf)
	write(t, filepath.Join(e.home, "proj", "evil.pdf"), "<html><script>fetch('/api/list')</script></html>")
	write(t, filepath.Join(e.home, "proj", "page.html"), "<h1>hi</h1>")

	resp := e.getRaw(t, "/api/pdf?path="+url.QueryEscape(filepath.Join(e.home, "proj", "doc.pdf")), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != pdf {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	h := resp.Header
	if h.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Errorf("Content-Type=%q Content-Disposition=%q", h.Get("Content-Type"), h.Get("Content-Disposition"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if h.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'self'") || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'none'") {
		t.Errorf("framing headers: XFO=%q CSP=%q", h.Get("X-Frame-Options"), h.Get("Content-Security-Policy"))
	}

	for _, name := range []string{"evil.pdf", "page.html"} {
		if code, _ := e.get(t, "/api/pdf", filepath.Join(e.home, "proj", name)); code != 415 {
			t.Errorf("%s: status = %d, want 415", name, code)
		}
	}
	// Range requests work: the viewer fetches large files in pieces.
	r2 := e.getRaw(t, "/api/pdf?path="+url.QueryEscape(filepath.Join(e.home, "proj", "doc.pdf")), map[string]string{"Range": "bytes=0-4"})
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 206 || string(b2) != "%PDF-" {
		t.Errorf("range: status=%d body=%q", r2.StatusCode, b2)
	}
}

func TestPDFEndpointObeysDenyRules(t *testing.T) {
	e := newEnv(t, false, nil)
	write(t, filepath.Join(e.home, ".ssh", "leaked.pdf"), "%PDF-1.4 SECRET")
	write(t, filepath.Join(e.home, "proj", "cert.pem"), "%PDF-1.4 SECRET")
	missing, _ := e.get(t, "/api/pdf", filepath.Join(e.home, "proj", "nope.pdf"))
	for _, p := range []string{filepath.Join(e.home, ".ssh", "leaked.pdf"), filepath.Join(e.home, "proj", "cert.pem")} {
		code, body := e.get(t, "/api/pdf", p)
		if code != 404 || code != missing || strings.Contains(body, "SECRET") {
			t.Errorf("%s: status=%d body=%q", p, code, body)
		}
	}
}
