package server

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/rules"
)

type env struct {
	home   string
	base   string // ts.URL
	client *http.Client
	ts     *httptest.Server
}

func write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newEnv(t testing.TB, debug bool, coreMissing []string) *env {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	write(t, filepath.Join(home, ".ssh", "id_ed25519"), "SECRET")
	write(t, filepath.Join(home, ".aws", "credentials"), "SECRET")
	write(t, filepath.Join(home, "proj", "hello.txt"), "hello")
	write(t, filepath.Join(home, "node_modules", "pkg", "index.js"), "js")
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "proj", "shortcut")); err != nil {
		t.Fatal(err)
	}

	deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	hide, err := rules.Parse(strings.NewReader(strings.Join(rules.DefaultIgnore, "\n")), rules.ParseOptions{Home: home, AllowNegation: true})
	if err != nil {
		t.Fatal(err)
	}
	g, err := guard.New([]string{home}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Guard: g, Debug: debug, CoreDenyMissing: coreMissing, Home: home})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	ts.Config.Handler = srv.Handler(port)
	ts.Start()
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Exchange the launch token for the session cookie, as a browser would.
	resp, err := client.Get(ts.URL + "/?token=" + srv.LaunchToken())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	return &env{home: home, base: ts.URL, client: client, ts: ts}
}

func (e *env) get(t *testing.T, endpoint, path string) (int, string) {
	t.Helper()
	resp, err := e.client.Get(e.base + endpoint + "?path=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// ndjsonNames parses a /api/list body: a {"path"} line, then {"entries"} lines.
func ndjsonNames(t *testing.T, body string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var head struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil || head.Path == "" {
		t.Fatalf("first line must be the path header, got %q (%v)", lines[0], err)
	}
	var names []string
	for _, l := range lines[1:] {
		var msg struct {
			Entries []struct{ Name string } `json:"entries"`
			Error   string                  `json:"error"`
		}
		if err := json.Unmarshal([]byte(l), &msg); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", l, err)
		}
		if msg.Error != "" {
			t.Fatalf("listing error line: %q", msg.Error)
		}
		for _, e := range msg.Entries {
			names = append(names, e.Name)
		}
	}
	return names
}

func TestListStreamsLargeDirectoriesInChunks(t *testing.T) {
	e := newEnv(t, false, nil)
	big := filepath.Join(e.home, "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	const n = listBatch*2 + 137
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(big, "f"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, body := e.get(t, "/api/list", big)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 4 { // header + at least 3 chunks
		t.Fatalf("expected the listing in several chunks, got %d lines", len(lines))
	}
	if got := len(ndjsonNames(t, body)); got != n {
		t.Fatalf("entries = %d, want %d", got, n)
	}
}

func TestEmptyDirectoryStillReturnsHeader(t *testing.T) {
	e := newEnv(t, false, nil)
	empty := filepath.Join(e.home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	code, body := e.get(t, "/api/list", empty)
	if code != 200 || len(ndjsonNames(t, body)) != 0 {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestEmbeddedUIIsServedToAuthenticatedClients(t *testing.T) {
	e := newEnv(t, false, nil)
	resp, err := e.client.Get(e.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `id="app"`) {
		t.Fatalf("status=%d, body does not look like the app shell: %.120q", resp.StatusCode, b)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Error("the app shell must carry the CSP")
	}
	// Unknown paths are a plain 404, and the UI is not served without a session.
	r2, _ := e.client.Get(e.base + "/no-such-asset.js")
	r2.Body.Close()
	if r2.StatusCode != 404 {
		t.Errorf("unknown asset status = %d", r2.StatusCode)
	}
	r3, _ := (&http.Client{}).Get(e.base + "/")
	r3.Body.Close()
	if r3.StatusCode != 403 {
		t.Errorf("unauthenticated UI status = %d, want 403", r3.StatusCode)
	}
}

func TestListHidesDeniedAndIgnored(t *testing.T) {
	e := newEnv(t, false, nil)
	code, body := e.get(t, "/api/list", e.home)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	got := ndjsonNames(t, body)
	if strings.Join(got, ",") != "proj" {
		t.Fatalf("entries = %v, want only [proj] (.ssh/.aws denied, node_modules hidden)", got)
	}
}

func TestEveryEndpointReturns404ForDeniedPaths(t *testing.T) {
	e := newEnv(t, false, nil)
	denied := []string{
		filepath.Join(e.home, ".ssh"),
		filepath.Join(e.home, ".ssh", "id_ed25519"),
		filepath.Join(e.home, ".aws", "credentials"),
		filepath.Join(e.home, "proj", "shortcut"),                 // symlink into .ssh
		filepath.Join(e.home, "proj", "shortcut", "id_ed25519"),   // file via symlink
		filepath.Join(e.home, "proj", "..", ".ssh", "id_ed25519"), // traversal
		e.home + "/proj/../.SSH/id_ed25519",                       // traversal + case
		e.home + "//.ssh//id_ed25519",
	}
	for _, endpoint := range []string{"/api/list", "/api/file"} {
		for _, p := range denied {
			code, body := e.get(t, endpoint, p)
			if code != 404 || strings.Contains(body, "SECRET") {
				t.Errorf("%s %q: status=%d body=%q, want 404 without secret", endpoint, p, code, body)
			}
		}
	}
}

// The response for a denied path must be byte-identical to that for a path
// that does not exist, or clients can probe for secrets.
func TestDeniedIsIndistinguishableFromMissing(t *testing.T) {
	e := newEnv(t, false, nil)
	for _, endpoint := range []string{"/api/list", "/api/file"} {
		dCode, dBody := e.get(t, endpoint, filepath.Join(e.home, ".ssh", "id_ed25519"))
		mCode, mBody := e.get(t, endpoint, filepath.Join(e.home, "proj", "does-not-exist"))
		if dCode != mCode || dBody != mBody {
			t.Errorf("%s: denied (%d %q) differs from missing (%d %q)", endpoint, dCode, dBody, mCode, mBody)
		}
	}
}

func TestDebugRevealsRuleOnlyForDeniedPaths(t *testing.T) {
	e := newEnv(t, true, nil)
	code, body := e.get(t, "/api/file", filepath.Join(e.home, ".ssh", "id_ed25519"))
	if code != 404 || !strings.Contains(body, ".ssh/") {
		t.Fatalf("debug denied: %d %q, want 404 naming the rule", code, body)
	}
	code, body = e.get(t, "/api/file", filepath.Join(e.home, "proj", "does-not-exist"))
	if code != 404 || strings.Contains(body, "rule") {
		t.Fatalf("debug missing: %d %q, must not mention a rule", code, body)
	}
}

func TestHiddenDirectoryStillReachable(t *testing.T) {
	e := newEnv(t, false, nil)
	if code, _ := e.get(t, "/api/list", filepath.Join(e.home, "node_modules")); code != 200 {
		t.Fatalf("hide rules must not block direct access, status = %d", code)
	}
	if code, body := e.get(t, "/api/file", filepath.Join(e.home, "node_modules", "pkg", "index.js")); code != 200 || body != "js" {
		t.Fatalf("status=%d body=%q", code, body)
	}
}

func TestFileServedSafely(t *testing.T) {
	e := newEnv(t, false, nil)
	resp, err := e.client.Get(e.base + "/api/file?path=" + url.QueryEscape(filepath.Join(e.home, "proj", "hello.txt")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "hello" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestDirectoryOnFileEndpointIs404(t *testing.T) {
	e := newEnv(t, false, nil)
	if code, _ := e.get(t, "/api/file", filepath.Join(e.home, "proj")); code != 404 {
		t.Fatalf("status = %d", code)
	}
}

func TestMalformedPathParameters(t *testing.T) {
	e := newEnv(t, false, nil)
	for _, p := range []string{"", "relative", "proj/hello.txt", "/", "/etc/passwd", "/nonexistent", e.home + "/proj/hello.txt\x00"} {
		for _, endpoint := range []string{"/api/list", "/api/file"} {
			code, _ := e.get(t, endpoint, p)
			if code != 404 {
				t.Errorf("%s %q: status = %d, want 404", endpoint, p, code)
			}
		}
	}
	// A request with no path parameter at all.
	resp, err := e.client.Get(e.base + "/api/file")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("missing parameter: status = %d", resp.StatusCode)
	}
}

func TestRawEncodedTraversalOnTheWire(t *testing.T) {
	e := newEnv(t, false, nil)
	secretDir := url.QueryEscape(e.home + "/proj/../.ssh/id_ed25519")
	for _, raw := range []string{
		"/api/file?path=" + secretDir,
		"/api/file?path=" + url.QueryEscape(url.QueryEscape(e.home+"/.ssh/id_ed25519")), // double-encoded
		"/api/file?path=" + strings.ReplaceAll(secretDir, "..", "%2e%2e"),
		"/api/file/../file?path=" + secretDir,
		"/api/./file?path=" + secretDir,
	} {
		resp, err := e.client.Get(e.base + raw)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(b), "SECRET") {
			t.Errorf("%s leaked the secret", raw)
		}
	}
}

func TestUnauthenticatedAndReadOnly(t *testing.T) {
	e := newEnv(t, false, nil)

	anon := &http.Client{}
	resp, err := anon.Get(e.base + "/api/list?path=" + url.QueryEscape(e.home))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("unauthenticated status = %d, want 403", resp.StatusCode)
	}

	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req, _ := http.NewRequest(m, e.base+"/api/file?path="+url.QueryEscape(filepath.Join(e.home, "proj", "hello.txt")), strings.NewReader("x"))
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Errorf("%s: status = %d, want 405", m, resp.StatusCode)
		}
	}
	// And the file is untouched.
	if b, _ := os.ReadFile(filepath.Join(e.home, "proj", "hello.txt")); string(b) != "hello" {
		t.Errorf("file was modified: %q", b)
	}
}

func TestForeignHostRejectedEvenWhenAuthenticated(t *testing.T) {
	e := newEnv(t, false, nil)
	req, _ := http.NewRequest("GET", e.base+"/api/list?path="+url.QueryEscape(e.home), nil)
	req.Host = "rebind.example:" + portOf(e.base)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func portOf(base string) string {
	u, _ := url.Parse(base)
	_, p, _ := net.SplitHostPort(u.Host)
	if _, err := strconv.Atoi(p); err != nil {
		return "0"
	}
	return p
}

func TestStatusReportsWeakenedCoreRules(t *testing.T) {
	e := newEnv(t, false, []string{"~/.ssh/", "~/.aws/"})
	resp, err := e.client.Get(e.base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		ReadOnly        bool     `json:"readOnly"`
		CoreDenyMissing []string `json:"coreDenyMissing"`
		Roots           []string `json:"roots"`
		Home            string   `json:"home"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if !st.ReadOnly || len(st.CoreDenyMissing) != 2 || len(st.Roots) != 1 || st.Roots[0] != e.home || st.Home != e.home {
		t.Fatalf("status = %+v (want 2 missing core rules and roots [%s])", st, e.home)
	}

	clean := newEnv(t, false, nil)
	resp2, err := clean.client.Get(clean.base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	b, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(b), `"coreDenyMissing":[]`) {
		t.Fatalf("a clean status must serialize an empty list, got %s", b)
	}
}

// ER-1: HTTP-facing packages reach the filesystem only through guard.Guard.
func TestHandlersDoNotImportFilesystemPackages(t *testing.T) {
	banned := map[string]bool{
		`"os"`: true, `"io/fs"`: true, `"io/ioutil"`: true,
		`"syscall"`: true, `"path/filepath"`: true, `"golang.org/x/sys/unix"`: true,
	}
	for _, dir := range []string{".", "../httpguard"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range af.Imports {
				if banned[imp.Path.Value] {
					t.Errorf("%s imports %s: filesystem access must go through guard.Guard", f, imp.Path.Value)
				}
			}
		}
	}
}
