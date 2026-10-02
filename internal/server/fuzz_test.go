package server

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzPathParameter sends arbitrary path and parameter values through the whole
// stack (middleware, handlers, guard) as an authenticated client, and checks
// only what must hold for ANY input: no response carries secret content, no
// listing names a secret, and every status is one the API is documented to use.
func FuzzPathParameter(f *testing.F) {
	// Quick Look is replaced by a stand-in that answers with the bytes it was
	// given to draw, so a secret that reached it would show in the response.
	e := newEnvWith(f, false, nil, func(c *Config) {
		c.Thumbnail = func(_ context.Context, _ int, prepare func(string) (string, error)) ([]byte, error) {
			dir, err := os.MkdirTemp("", "fsb-fuzz-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(dir)
			p, err := prepare(dir)
			if err != nil {
				return nil, err
			}
			var all []byte
			filepath.WalkDir(p, func(f string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					b, _ := os.ReadFile(f)
					all = append(all, b...)
				}
				return nil
			})
			return append([]byte("\x89PNG\r\n\x1a\n"), all...), nil
		}
	})
	write(f, filepath.Join(e.home, ".ssh", "nested", "deeper.txt"), "SECRET-NESTED")
	write(f, filepath.Join(e.home, "proj", "public.txt"), "public")
	write(f, filepath.Join(e.home, ".ssh", "notes.docx"), "SECRET-DOCX")
	write(f, filepath.Join(e.home, "proj", "report.docx"), "public docx")
	write(f, filepath.Join(e.home, "proj", "Budget.numbers", "Index.zip"), "public")
	write(f, filepath.Join(e.home, "proj", "Budget.numbers", "Data", ".env"), "SECRET-IN-PACKAGE")
	if err := os.Symlink(filepath.Join(e.home, ".ssh", "notes.docx"), filepath.Join(e.home, "proj", "link.docx")); err != nil {
		f.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.home, ".ssh"), filepath.Join(e.home, "proj", "Budget.numbers", "keys")); err != nil {
		f.Fatal(err)
	}

	home := e.home
	for _, seed := range []string{
		home + "/.ssh/id_ed25519", home + "/.ssh", home + "/.ssh/nested/deeper.txt",
		home + "/proj/../.ssh/id_ed25519", home + "/proj/shortcut/id_ed25519", home + "/proj/shortcut",
		home + "/.SSH/id_ed25519", home + "//.ssh//id_ed25519", home + "/.ssh/../.ssh/id_ed25519",
		"/System/Volumes/Data" + home + "/.ssh/id_ed25519", "/private" + home + "/.ssh/id_ed25519",
		home + "/.ssh/id_ed25519\x00", home + "/.ssh/id_ed25519/..namedfork/rsrc",
		home + "/proj/public.txt", home + "/proj", home, "/", "", "%2e%2e/%2e%2e/etc/passwd",
		"../../../../etc/passwd", strings.Repeat("a", 5000), home + strings.Repeat("/proj/..", 200) + "/.ssh/id_ed25519",
		home + "/.ssh/notes.docx", home + "/proj/link.docx", home + "/proj/report.docx", home + "/proj/Budget.numbers",
		home + "/proj/Budget.numbers/keys/notes.docx", home + "/proj/shortcut/notes.docx",
	} {
		f.Add(seed, "id", "64")
	}

	ok := map[int]bool{200: true, 206: true, 400: true, 403: true, 404: true, 405: true, 409: true, 415: true, 416: true}
	f.Fuzz(func(t *testing.T, path, query, bytesParam string) {
		for _, endpoint := range []string{"/api/list", "/api/file", "/api/head", "/api/meta", "/api/preview", "/api/pdf", "/api/archive", "/api/search", "/api/quicklook"} {
			params := url.Values{"path": {path}, "q": {query}, "bytes": {bytesParam}, "values": {bytesParam}}
			resp, err := e.client.Get(e.base + endpoint + "?" + params.Encode())
			if err != nil {
				// The client refusing to send an unusable URL is not a server fault.
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if !ok[resp.StatusCode] {
				t.Fatalf("%s path=%q: unexpected status %d", endpoint, path, resp.StatusCode)
			}
			s := string(body)
			if strings.Contains(s, "SECRET") {
				t.Fatalf("%s path=%q leaked secret content: %.200q", endpoint, path, s)
			}
			// Listings and search results must never name a denied entry.
			if (endpoint == "/api/list" || endpoint == "/api/search") && resp.StatusCode == 200 {
				if strings.Contains(s, `"name":".ssh"`) || strings.Contains(s, `"name":".aws"`) || strings.Contains(s, "id_ed25519") {
					t.Fatalf("%s path=%q named a denied entry: %.300q", endpoint, path, s)
				}
			}
		}
	})
}
