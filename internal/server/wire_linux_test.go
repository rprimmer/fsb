//go:build linux

package server

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file whose name is not UTF-8 is listed, found and described by a name
// the browser can send back: escaped in JSON, and requested by its real bytes.
func TestNonUTF8NameRoundTrips(t *testing.T) {
	e := newEnv(t, false, nil)
	dir := filepath.Join(e.home, "lat\xe9n")
	write(t, filepath.Join(dir, "caf\xe9.txt"), "bonjour")
	wire := "caf\x00E9.txt"
	wireDir := filepath.Join(e.home, "lat\x00E9n")

	code, body := e.get(t, "/api/list", dir)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if !strings.Contains(body, `{"path":`+jsonString(t, wireDir)+`}`) {
		t.Errorf("list header should name the folder as %q:\n%s", wireDir, body)
	}
	if names := ndjsonNames(t, body); len(names) != 1 || names[0] != wire {
		t.Errorf("list names = %q, want [%q]", names, wire)
	}

	code, body = e.get(t, "/api/head", filepath.Join(dir, "caf\xe9.txt"))
	if code != 200 || !strings.Contains(body, "bonjour") {
		t.Errorf("head by real bytes: %d %s", code, body)
	}

	code, body = e.get(t, "/api/meta", filepath.Join(dir, "caf\xe9.txt"))
	var m struct{ Name, Path string }
	if code != 200 || json.Unmarshal([]byte(body), &m) != nil || m.Name != wire || m.Path != filepath.Join(wireDir, wire) {
		t.Errorf("meta: %d name %q path %q; want %q %q", code, m.Name, m.Path, wire, filepath.Join(wireDir, wire))
	}

	code, body = e.getQuery(t, "/api/search", url.Values{"path": {e.home}, "q": {"caf"}})
	if code != 200 || !strings.Contains(body, jsonString(t, filepath.Join(wireDir, wire))) {
		t.Errorf("search should report %q: %d %s", filepath.Join(wireDir, wire), code, body)
	}

	if err := os.Symlink(filepath.Join(dir, "caf\xe9.txt"), filepath.Join(e.home, "link")); err != nil {
		t.Fatal(err)
	}
	_, body = e.get(t, "/api/meta", filepath.Join(e.home, "link"))
	if !strings.Contains(body, `"symlinkTarget":`+jsonString(t, filepath.Join(wireDir, wire))) {
		t.Errorf("symlink target should be escaped: %s", body)
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
