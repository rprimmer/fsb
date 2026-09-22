package server

import (
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/rules"
)

// A real bug, found from a screenshot of the running app: selecting a UNIX
// domain socket in the interface showed "Server error (500)." instead of the
// ordinary "not found" every other special file gets (guard.ErrNotRegular
// went unrecognized once the underlying open() failure was an errno the
// guard's mapErr did not know, and fell to the server's default 500 case; see
// TestSocketFileIsRefusedLikeAnyOtherSpecialFileNotWithAnInternalError in
// internal/guard). This is the same bug reproduced through the HTTP endpoints
// the browser actually calls.
//
// A real socket's bind path has a short OS limit (about 104 bytes on macOS),
// which newEnv's path under t.TempDir() can exceed, so this builds its own
// short-rooted server rather than reusing newEnv.
func TestSocketFileAtHTTPLevelAnswersNotFoundNotInternalError(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "fsbsock")
	if err != nil {
		t.Skipf("no writable short-path temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	write(t, filepath.Join(root, "proj", "hello.txt"), "hello")
	sockPath := filepath.Join(root, "proj", "app.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix sockets unsupported here: %v", err)
	}
	defer ln.Close()

	deny, err := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: root})
	if err != nil {
		t.Fatal(err)
	}
	g, err := guard.New([]string{root}, deny, deny)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Guard: g, Home: root})
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
	resp, err := client.Get(ts.URL + srv.LaunchPath() + "?token=" + srv.LaunchToken())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	base := ts.URL + strings.TrimSuffix(srv.LaunchPath(), "/")
	e := &env{home: root, base: base, client: client, ts: ts}

	missing, _ := e.get(t, "/api/head", filepath.Join(root, "proj", "nope"))
	for _, endpoint := range []string{"/api/head", "/api/meta", "/api/preview", "/api/pdf", "/api/archive", "/api/file"} {
		code, body := e.get(t, endpoint, sockPath)
		if code == http.StatusInternalServerError {
			t.Errorf("%s: status = 500 (the original bug); body: %s", endpoint, body)
			continue
		}
		if code != missing {
			t.Errorf("%s: status = %d, want %d (same as a missing path)", endpoint, code, missing)
		}
	}
	code, body := e.get(t, "/api/list", filepath.Join(root, "proj"))
	if code != 200 || !strings.Contains(body, "app.sock") {
		t.Errorf("the socket should still be listed: status=%d body=%s", code, body)
	}
}
