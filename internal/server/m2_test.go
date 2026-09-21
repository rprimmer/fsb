package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01")

func (e *env) getRaw(t *testing.T, target string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", e.base+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *env) getQuery(t *testing.T, endpoint string, params url.Values) (int, string) {
	t.Helper()
	resp := e.getRaw(t, endpoint+"?"+params.Encode(), nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHeadEndpoint(t *testing.T) {
	e := newEnv(t, false, nil)
	write(t, filepath.Join(e.home, "proj", "notes.txt"), "line one\nline two\n")
	if err := os.WriteFile(filepath.Join(e.home, "proj", "blob.bin"), []byte("ab\x00cd\xff"), 0o644); err != nil {
		t.Fatal(err)
	}

	type head struct {
		Kind      string `json:"kind"`
		Size      int64  `json:"size"`
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}
	get := func(name string, extra url.Values) (int, head) {
		params := url.Values{"path": {filepath.Join(e.home, "proj", name)}}
		for k, v := range extra {
			params[k] = v
		}
		code, body := e.getQuery(t, "/api/head", params)
		var h head
		json.Unmarshal([]byte(body), &h)
		return code, h
	}

	if code, h := get("notes.txt", nil); code != 200 || h.Kind != "text" || h.Text != "line one\nline two\n" || h.Truncated {
		t.Errorf("text: %d %+v", code, h)
	}
	if code, h := get("notes.txt", url.Values{"bytes": {"4"}}); code != 200 || h.Text != "line" || !h.Truncated {
		t.Errorf("bytes=4: %d %+v", code, h)
	}
	if code, h := get("blob.bin", nil); code != 200 || h.Kind != "binary" || h.Text != "" || h.Size != 6 {
		t.Errorf("binary must report kind and size only: %d %+v", code, h)
	}
	for _, bad := range []string{"0", "-1", "abc", "1e3", "65537", "99999999999999999999"} {
		if code, _ := get("notes.txt", url.Values{"bytes": {bad}}); code != 400 {
			t.Errorf("bytes=%s: status = %d, want 400", bad, code)
		}
	}
	if code, _ := get("notes.txt", url.Values{"bytes": {"65536"}}); code != 200 {
		t.Errorf("the maximum must be accepted: %d", code)
	}
}

func TestBinaryContentNeverAppearsInHeadResponses(t *testing.T) {
	e := newEnv(t, false, nil)
	secretish := append([]byte{0x00, 0xff, 0xfe}, []byte("MAGIC-BYTES-SHOULD-NOT-LEAK")...)
	if err := os.WriteFile(filepath.Join(e.home, "proj", "blob.bin"), secretish, 0o644); err != nil {
		t.Fatal(err)
	}
	_, body := e.get(t, "/api/head", filepath.Join(e.home, "proj", "blob.bin"))
	if strings.Contains(body, "MAGIC") {
		t.Fatalf("binary file contents leaked through /api/head: %s", body)
	}
}

func TestMetaEndpoint(t *testing.T) {
	e := newEnv(t, false, nil)
	code, body := e.get(t, "/api/meta", filepath.Join(e.home, "proj", "hello.txt"))
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	var m struct {
		Name   string            `json:"name"`
		Size   int64             `json:"size"`
		XAttrs []json.RawMessage `json:"xattrs"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil || m.Name != "hello.txt" || m.Size != 5 || m.XAttrs == nil {
		t.Fatalf("meta = %+v (%v) from %s", m, err, body)
	}
	if code, _ := e.getQuery(t, "/api/meta", url.Values{"path": {filepath.Join(e.home, "proj", "hello.txt")}, "values": {"0"}}); code != 200 {
		t.Errorf("values=0: %d", code)
	}
}

func TestPreviewServesOnlySandboxedImages(t *testing.T) {
	e := newEnv(t, false, nil)
	if err := os.WriteFile(filepath.Join(e.home, "proj", "pic.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.home, "proj", "evil.png"), "<html><script>fetch('/api/list')</script></html>")
	write(t, filepath.Join(e.home, "proj", "vector.svg"), `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)
	write(t, filepath.Join(e.home, "proj", "page.html"), "<h1>hi</h1>")

	resp := e.getRaw(t, "/api/preview?path="+url.QueryEscape(filepath.Join(e.home, "proj", "pic.png")), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(pngBytes) {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	h := resp.Header
	if h.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
	if !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Errorf("Content-Disposition = %q", h.Get("Content-Disposition"))
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("an inline image must be served sandboxed, CSP = %q", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}

	for _, name := range []string{"evil.png", "vector.svg", "page.html"} {
		code, _ := e.get(t, "/api/preview", filepath.Join(e.home, "proj", name))
		if code != 415 {
			t.Errorf("%s: status = %d, want 415 (never inline)", name, code)
		}
	}

	// Range requests work (large images, partial loads).
	r2 := e.getRaw(t, "/api/preview?path="+url.QueryEscape(filepath.Join(e.home, "proj", "pic.png")), map[string]string{"Range": "bytes=0-3"})
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 206 || len(b2) != 4 {
		t.Errorf("range: status=%d len=%d", r2.StatusCode, len(b2))
	}
}

func TestNewEndpointsReturn404ForDeniedPathsAndMatchMissingOnes(t *testing.T) {
	e := newEnv(t, false, nil)
	if err := os.WriteFile(filepath.Join(e.home, ".ssh", "photo.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	denied := []string{
		filepath.Join(e.home, ".ssh"),
		filepath.Join(e.home, ".ssh", "id_ed25519"),
		filepath.Join(e.home, ".ssh", "photo.png"),
		filepath.Join(e.home, "proj", "shortcut", "id_ed25519"),
		filepath.Join(e.home, "proj", "..", ".ssh", "id_ed25519"),
		e.home + "/.SSH/id_ed25519",
	}
	for _, endpoint := range []string{"/api/head", "/api/meta", "/api/preview"} {
		for _, p := range denied {
			code, body := e.get(t, endpoint, p)
			if code != 404 || strings.Contains(body, "SECRET") {
				t.Errorf("%s %q: status=%d body=%q, want a plain 404", endpoint, p, code, body)
			}
		}
		dCode, dBody := e.get(t, endpoint, filepath.Join(e.home, ".ssh", "id_ed25519"))
		mCode, mBody := e.get(t, endpoint, filepath.Join(e.home, "proj", "does-not-exist"))
		if dCode != mCode || dBody != mBody {
			t.Errorf("%s: denied (%d %q) differs from missing (%d %q)", endpoint, dCode, dBody, mCode, mBody)
		}
	}
	for _, root := range denied[:2] {
		code, body := e.getQuery(t, "/api/search", url.Values{"path": {root}, "q": {"id"}})
		if code != 404 || strings.Contains(body, "SECRET") {
			t.Errorf("/api/search root %q: status=%d body=%q", root, code, body)
		}
	}
	dCode, dBody := e.getQuery(t, "/api/search", url.Values{"path": {filepath.Join(e.home, ".ssh")}, "q": {"id"}})
	mCode, mBody := e.getQuery(t, "/api/search", url.Values{"path": {filepath.Join(e.home, "nope")}, "q": {"id"}})
	if dCode != mCode || dBody != mBody {
		t.Errorf("search: denied root (%d %q) differs from missing root (%d %q)", dCode, dBody, mCode, mBody)
	}
}

// Hovering exposes contents more readily than clicking, so secret files are
// core deny rules: they must be unreachable through every endpoint, including
// the head request that powers the hover bubble.
func TestSecretFilesAreDeniedEverywhereByDefault(t *testing.T) {
	e := newEnv(t, false, nil)
	secrets := []string{
		filepath.Join(e.home, "proj", ".env"),
		filepath.Join(e.home, "proj", ".env.local"),
		filepath.Join(e.home, "proj", "server.pem"),
		filepath.Join(e.home, "proj", "id.key"),
	}
	for _, p := range secrets {
		write(t, p, "TOP-SECRET-VALUE")
	}
	write(t, filepath.Join(e.home, "proj", ".envrc"), "not a secret file name")

	// Not listed.
	_, body := e.get(t, "/api/list", filepath.Join(e.home, "proj"))
	names := strings.Join(ndjsonNames(t, body), ",")
	for _, n := range []string{".env", ".env.local", "server.pem", "id.key"} {
		for _, listed := range strings.Split(names, ",") {
			if listed == n {
				t.Errorf("%s must not be listed (got %s)", n, names)
			}
		}
	}
	if !strings.Contains(names, ".envrc") || !strings.Contains(names, "hello.txt") {
		t.Errorf("unrelated files must still be listed: %s", names)
	}

	// Not reachable by any endpoint, and never echoed.
	for _, endpoint := range []string{"/api/file", "/api/head", "/api/meta", "/api/preview", "/api/list"} {
		for _, p := range secrets {
			code, b := e.get(t, endpoint, p)
			if code != 404 || strings.Contains(b, "TOP-SECRET") {
				t.Errorf("%s %s: status=%d body=%q, want a plain 404", endpoint, filepath.Base(p), code, b)
			}
		}
	}

	// Not searchable.
	for _, q := range []string{"env", "server", "id.key", ".pem"} {
		_, sb := e.getQuery(t, "/api/search", url.Values{"path": {e.home}, "q": {q}})
		rels, _ := parseSearch(t, sb)
		for _, r := range rels {
			if strings.HasSuffix(r, ".env") || strings.HasSuffix(r, ".env.local") || strings.HasSuffix(r, ".pem") || strings.HasSuffix(r, ".key") {
				t.Errorf("search %q reported a denied secret file: %s", q, r)
			}
		}
	}
	// The similarly named, non-secret file is still reachable (no over-blocking).
	if code, _ := e.get(t, "/api/head", filepath.Join(e.home, "proj", ".envrc")); code != 200 {
		t.Errorf(".envrc must remain readable, status = %d", code)
	}
}

func TestDebugNamesTheRuleOnNewEndpointsToo(t *testing.T) {
	e := newEnv(t, true, nil)
	for _, endpoint := range []string{"/api/head", "/api/meta", "/api/preview"} {
		if code, body := e.get(t, endpoint, filepath.Join(e.home, ".ssh", "id_ed25519")); code != 404 || !strings.Contains(body, "~/.ssh/") {
			t.Errorf("%s in debug mode: %d %q", endpoint, code, body)
		}
	}
}

type searchLine struct {
	Path      string                       `json:"path"`
	Matches   []struct{ Rel, Name string } `json:"matches"`
	Done      bool                         `json:"done"`
	Visited   int                          `json:"visited"`
	Truncated bool                         `json:"truncated"`
	Error     string                       `json:"error"`
}

func parseSearch(t *testing.T, body string) (rels []string, done searchLine) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var head searchLine
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil || head.Path == "" {
		t.Fatalf("first line must be the header, got %q (%v)", lines[0], err)
	}
	for _, l := range lines[1:] {
		var sl searchLine
		if err := json.Unmarshal([]byte(l), &sl); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", l, err)
		}
		if sl.Error != "" {
			t.Fatalf("search error line: %q", sl.Error)
		}
		for _, m := range sl.Matches {
			rels = append(rels, m.Rel)
		}
		if sl.Done {
			done = sl
		}
	}
	return rels, done
}

func TestSearchEndpoint(t *testing.T) {
	e := newEnv(t, false, nil)
	write(t, filepath.Join(e.home, "proj", "docs", "needle-one.txt"), "x")
	write(t, filepath.Join(e.home, "proj", "needle-two.txt"), "x")
	write(t, filepath.Join(e.home, ".ssh", "needle-secret"), "SECRET")
	write(t, filepath.Join(e.home, "node_modules", "needle-hidden.js"), "x")

	code, body := e.getQuery(t, "/api/search", url.Values{"path": {e.home}, "q": {"NEEDLE"}})
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	rels, done := parseSearch(t, body)
	if strings.Join(rels, "|") != "proj/needle-two.txt|proj/docs/needle-one.txt" {
		t.Errorf("matches = %v (shallow first; denied and hidden entries excluded)", rels)
	}
	if !done.Done || done.Truncated || done.Visited < 4 {
		t.Errorf("done line = %+v", done)
	}
	if strings.Contains(body, "SECRET") || strings.Contains(body, "needle-secret") || strings.Contains(body, "needle-hidden") {
		t.Errorf("denied or hidden entries leaked into search results: %s", body)
	}

	// No matches still produces a well-formed stream.
	code, body = e.getQuery(t, "/api/search", url.Values{"path": {e.home}, "q": {"zzz-nothing"}})
	rels, done = parseSearch(t, body)
	if code != 200 || len(rels) != 0 || !done.Done {
		t.Errorf("no-match search: %d %v %+v", code, rels, done)
	}
	// An empty query is not an error and finds nothing.
	code, body = e.getQuery(t, "/api/search", url.Values{"path": {e.home}, "q": {""}})
	rels, done = parseSearch(t, body)
	if code != 200 || len(rels) != 0 || !done.Done {
		t.Errorf("empty query: %d %v %+v", code, rels, done)
	}
}

func TestNewEndpointsRequireASessionAndAreReadOnly(t *testing.T) {
	e := newEnv(t, false, nil)
	anon := &http.Client{}
	for _, endpoint := range []string{"/api/head", "/api/meta", "/api/preview", "/api/search"} {
		target := e.base + endpoint + "?path=" + url.QueryEscape(filepath.Join(e.home, "proj", "hello.txt")) + "&q=x"
		resp, err := anon.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s unauthenticated: %d, want 403", endpoint, resp.StatusCode)
		}
		req, _ := http.NewRequest("POST", target, strings.NewReader("x"))
		resp, err = e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Errorf("%s POST: %d, want 405", endpoint, resp.StatusCode)
		}
	}
}

func TestSearchEndpointMatchCase(t *testing.T) {
	e := newEnv(t, false, nil)
	write(t, filepath.Join(e.home, "proj", "Report-final.txt"), "x")
	write(t, filepath.Join(e.home, "proj", "report-draft.txt"), "x")

	count := func(params url.Values) int {
		code, body := e.getQuery(t, "/api/search", params)
		if code != 200 {
			t.Fatalf("status = %d", code)
		}
		rels, _ := parseSearch(t, body)
		return len(rels)
	}
	base := url.Values{"path": {e.home}, "q": {"Report"}}
	if n := count(base); n != 2 {
		t.Errorf("default (case-insensitive): %d results, want 2", n)
	}
	for _, v := range []string{"1"} {
		p := url.Values{"path": {e.home}, "q": {"Report"}, "case": {v}}
		if n := count(p); n != 1 {
			t.Errorf("case=%s: %d results, want 1", v, n)
		}
	}
	// Anything but "1" means the default.
	for _, v := range []string{"0", "", "true", "yes"} {
		p := url.Values{"path": {e.home}, "q": {"Report"}, "case": {v}}
		if n := count(p); n != 2 {
			t.Errorf("case=%q: %d results, want 2 (only 1 turns match-case on)", v, n)
		}
	}
}
