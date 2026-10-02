package server

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

var fakePNG = []byte("\x89PNG\r\n\x1a\nfake")

func TestQuickLookServesASandboxedPictureOfAnOfficeDocument(t *testing.T) {
	var asked []string
	e := newEnvWith(t, false, nil, func(c *Config) {
		c.Thumbnail = func(_ context.Context, path string, size int) ([]byte, error) {
			asked = append(asked, path)
			return fakePNG, nil
		}
	})
	doc := filepath.Join(e.home, "proj", "report.docx")
	write(t, doc, "PK fake docx")

	resp, err := e.client.Get(e.base + "/api/quicklook?path=" + url.QueryEscape(doc))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/png",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(asked) != 1 || asked[0] != doc {
		t.Errorf("Quick Look was asked for %q, want [%q]", asked, doc)
	}

	// Never handed to Quick Look: other types, denied and missing paths.
	asked = nil
	write(t, filepath.Join(e.home, "proj", "server.key"), "SECRET")
	for path, want := range map[string]int{
		filepath.Join(e.home, "proj", "hello.txt"):  415,
		filepath.Join(e.home, "proj", "server.key"): 404, // *.key is a core deny rule (Keynote files too)
		filepath.Join(e.home, "proj", "gone.docx"):  404,
		filepath.Join(e.home, ".ssh", "id.docx"):    404,
	} {
		if code, _ := e.get(t, "/api/quicklook", path); code != want {
			t.Errorf("%s: status %d, want %d", path, code, want)
		}
	}
	if len(asked) != 0 {
		t.Errorf("Quick Look was asked for %q", asked)
	}
}

func TestQuickLookPackageDocuments(t *testing.T) {
	e := newEnvWith(t, false, nil, func(c *Config) {
		c.Thumbnail = func(context.Context, string, int) ([]byte, error) { return fakePNG, nil }
	})
	pkg := filepath.Join(e.home, "proj", "Budget.numbers")
	write(t, filepath.Join(pkg, "Index.zip"), "PK")
	write(t, filepath.Join(pkg, "Data", "a.jpg"), "x")
	if code, _ := e.get(t, "/api/quicklook", pkg); code != 200 {
		t.Fatalf("package document: status %d", code)
	}
	// A link inside a package could lead Quick Look anywhere.
	if err := os.Symlink(filepath.Join(e.home, ".ssh", "id_ed25519"), filepath.Join(pkg, "Data", "b.jpg")); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.get(t, "/api/quicklook", pkg); code != 415 {
		t.Errorf("package holding a symlink: status %d, want 415", code)
	}
	os.Remove(filepath.Join(pkg, "Data", "b.jpg"))
	// So could a secret inside it.
	write(t, filepath.Join(pkg, "Data", ".env"), "SECRET=1")
	if code, _ := e.get(t, "/api/quicklook", pkg); code != 404 {
		t.Errorf("package holding a denied file: status %d, want 404", code)
	}
}

func TestQuickLookDiscardsThePictureIfTheDocumentWasSwapped(t *testing.T) {
	var doc string
	e := newEnvWith(t, false, nil, func(c *Config) {
		c.Thumbnail = func(context.Context, string, int) ([]byte, error) {
			// While Quick Look reads by path, something replaces the document.
			os.Remove(doc)
			write(t, doc, "something else entirely")
			return fakePNG, nil
		}
	})
	doc = filepath.Join(e.home, "proj", "report.docx")
	write(t, doc, "PK fake docx")
	if code, body := e.get(t, "/api/quicklook", doc); code != 404 || body == string(fakePNG) {
		t.Errorf("status %d; the picture of a swapped document must not be served", code)
	}
}

func TestQuickLookFailureIsUnsupported(t *testing.T) {
	e := newEnvWith(t, false, nil, func(c *Config) {
		c.Thumbnail = func(context.Context, string, int) ([]byte, error) { return nil, errors.New("no generator") }
	})
	doc := filepath.Join(e.home, "proj", "old.doc")
	write(t, doc, "x")
	if code, _ := e.get(t, "/api/quicklook", doc); code != 415 {
		t.Errorf("status %d, want 415", code)
	}
}
