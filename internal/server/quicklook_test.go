package server

import (
	"context"
	"errors"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rprimmer/fsb/internal/quicklook"
	"golang.org/x/sys/unix"
)

var fakePNG = []byte("\x89PNG\r\n\x1a\nfake")

// handedOver records what Quick Look would have been given: the path, and the
// files of the copy with their contents.
type handedOver struct {
	path  string
	files map[string]string
}

// fakeQuickLook prepares the document as the real Thumbnail does, records it,
// and draws nothing.
func fakeQuickLook(t *testing.T, got *[]handedOver) func(context.Context, int, func(string) (string, error)) ([]byte, error) {
	return func(_ context.Context, _ int, prepare func(string) (string, error)) ([]byte, error) {
		dir := t.TempDir()
		p, err := prepare(dir)
		if err != nil {
			return nil, err
		}
		h := handedOver{path: p, files: map[string]string{}}
		filepath.WalkDir(p, func(f string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(f)
				rel, _ := filepath.Rel(p, f)
				h.files[rel] = string(b)
			}
			return nil
		})
		*got = append(*got, h)
		return fakePNG, nil
	}
}

func TestQuickLookIsGivenAPrivateCopyNeverTheUsersPath(t *testing.T) {
	var got []handedOver
	e := newEnvWith(t, false, nil, func(c *Config) { c.Thumbnail = fakeQuickLook(t, &got) })
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
		"Cache-Control":           "no-store",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(got) != 1 || strings.HasPrefix(got[0].path, e.home) || filepath.Ext(got[0].path) != ".docx" || got[0].files["."] != "PK fake docx" {
		t.Errorf("Quick Look was given %+v; want a private .docx copy of the document", got)
	}

	// Never prepared: other types, denied and missing paths.
	got = nil
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
	if len(got) != 0 {
		t.Errorf("Quick Look was given %+v", got)
	}
}

// A Finder alias is a regular file that Quick Look follows to its target. Its
// mark is the extended attribute com.apple.FinderInfo; the copy has no
// extended attributes at all, so there is nothing to follow.
func TestQuickLookCopyCarriesNoExtendedAttributes(t *testing.T) {
	var got []handedOver
	e := newEnvWith(t, false, nil, func(c *Config) { c.Thumbnail = fakeQuickLook(t, &got) })
	doc := filepath.Join(e.home, "proj", "report.docx")
	write(t, doc, "bookmark bytes")
	finderInfo := append([]byte("alisMACS\x80\x00"), make([]byte, 22)...)
	if err := unix.Setxattr(doc, "com.apple.FinderInfo", finderInfo, 0); err != nil {
		t.Skipf("cannot set extended attributes here: %v", err)
	}
	if code, _ := e.get(t, "/api/quicklook", doc); code != 200 || len(got) != 1 {
		t.Fatalf("status %d, handed over %d", code, len(got))
	}
	// macOS may add its own com.apple.provenance to any new file; what matters
	// is that nothing of the original's comes along.
	for _, name := range []string{"com.apple.FinderInfo", "com.apple.ResourceFork"} {
		if n, err := unix.Getxattr(got[0].path, name, nil); err == nil {
			t.Errorf("the copy carries %s (%d bytes)", name, n)
		}
	}
}

func TestQuickLookPackageCopyLeavesOutWhatAListingLeavesOut(t *testing.T) {
	var got []handedOver
	e := newEnvWith(t, false, nil, func(c *Config) { c.Thumbnail = fakeQuickLook(t, &got) })
	pkg := filepath.Join(e.home, "proj", "Budget.numbers")
	write(t, filepath.Join(pkg, "Index.zip"), "PK")
	write(t, filepath.Join(pkg, "Data", "a.jpg"), "jpeg")
	write(t, filepath.Join(pkg, "Data", ".env"), "SECRET=1")                                                             // denied
	if err := os.Symlink(filepath.Join(e.home, ".ssh", "id_ed25519"), filepath.Join(pkg, "Data", "b.jpg")); err != nil { // a link
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.home, ".ssh"), filepath.Join(pkg, "keys")); err != nil { // a link to a folder
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(e.home, ".aws", "credentials"), filepath.Join(pkg, "Data", "c.jpg")); err != nil { // hard link to a secret
		t.Fatal(err)
	}

	clean := filepath.Join(e.home, "proj", "Clean.numbers")
	write(t, filepath.Join(clean, "Index.zip"), "PK")
	write(t, filepath.Join(clean, "Data", "a.jpg"), "jpeg")

	for _, p := range []string{pkg, clean} {
		if code, _ := e.get(t, "/api/quicklook", p); code != 200 {
			t.Errorf("%s: status %d, want 200 (a package with hidden parts must answer as a clean one does)", p, code)
		}
	}
	if len(got) != 2 {
		t.Fatalf("handed over %d", len(got))
	}
	var names []string
	for n, body := range got[0].files {
		names = append(names, n)
		if strings.Contains(body, "SECRET") {
			t.Errorf("the copy holds a secret in %s", n)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "Data/a.jpg,Index.zip" {
		t.Errorf("copied %q, want only Data/a.jpg and Index.zip", names)
	}
}

func TestQuickLookFailureIsUnsupported(t *testing.T) {
	e := newEnvWith(t, false, nil, func(c *Config) {
		c.Thumbnail = func(_ context.Context, _ int, prepare func(string) (string, error)) ([]byte, error) {
			if _, err := prepare(t.TempDir()); err != nil {
				return nil, err
			}
			return nil, errors.New("no generator")
		}
	})
	doc := filepath.Join(e.home, "proj", "old.doc")
	write(t, doc, "x")
	if code, _ := e.get(t, "/api/quicklook", doc); code != 415 {
		t.Errorf("status %d, want 415", code)
	}
}

// The reviewer's finding, end to end with the real Quick Look: an alias named
// .docx that points into a denied folder must not be drawn. Needs swiftc to
// make a real alias, and qlmanage.
func TestQuickLookDoesNotFollowAFinderAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a helper and runs Quick Look")
	}
	swiftc, err1 := exec.LookPath("swiftc")
	_, err2 := exec.LookPath("qlmanage")
	if err1 != nil || err2 != nil {
		t.Skip("needs swiftc and qlmanage (macOS with developer tools)")
	}
	tools := t.TempDir()
	src := filepath.Join(tools, "alias.swift")
	os.WriteFile(src, []byte(`import Foundation
let a = CommandLine.arguments
let data = try URL(fileURLWithPath: a[1]).bookmarkData(options: [.suitableForBookmarkFile], includingResourceValuesForKeys: nil, relativeTo: nil)
try URL.writeBookmarkData(data, to: URL(fileURLWithPath: a[2]))
`), 0o644)
	mkalias := filepath.Join(tools, "mkalias")
	if out, err := exec.Command(swiftc, "-o", mkalias, src).CombinedOutput(); err != nil {
		t.Skipf("swiftc failed: %v\n%s", err, out)
	}

	e := newEnvWith(t, false, nil, func(c *Config) { c.Thumbnail = quicklook.Thumbnail })
	secret := filepath.Join(e.home, ".ssh", "notes.txt")
	write(t, secret, "SSH-FOLDER-SECRET\n")
	outside := filepath.Join(filepath.Dir(e.home), "outside.txt")
	write(t, outside, "OUTSIDE-THE-ROOTS\n")
	for name, target := range map[string]string{"c.xls": secret, "budget.xlsx": outside} {
		alias := filepath.Join(e.home, "proj", name)
		if out, err := exec.Command(mkalias, target, alias).CombinedOutput(); err != nil {
			t.Fatalf("mkalias: %v\n%s", err, out)
		}
		// Quick Look does follow the alias itself: the hole is real.
		if out, _ := exec.Command("xattr", "-px", "com.apple.FinderInfo", alias).Output(); !strings.HasPrefix(string(out), "61 6C 69 73") {
			t.Fatalf("%s is not marked as an alias: %q", name, out)
		}
		// The copy is not an alias, so Quick Look sees only the alias file's own
		// bytes, which it cannot draw: at most a blank page, never the target's
		// text.
		if code, body := e.get(t, "/api/quicklook", alias); code == 200 && hasInk(t, body) {
			t.Errorf("%s (an alias to %s): served a picture with content (%d bytes)", name, target, len(body))
		}
	}
}

// hasInk reports whether a PNG has any clearly non-white pixel.
func hasInk(t *testing.T, data string) bool {
	t.Helper()
	img, err := png.Decode(strings.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0x8000 && (r < 0xc000 || g < 0xc000 || bl < 0xc000) {
				return true
			}
		}
	}
	return false
}

// Copying a document for Quick Look has its own time limit; when it runs out
// the answer is the same as for a document Quick Look cannot draw.
func TestQuickLookCopyHasATimeLimit(t *testing.T) {
	old := quickLookCopyTimeout
	quickLookCopyTimeout = time.Nanosecond
	defer func() { quickLookCopyTimeout = old }()
	var got []handedOver
	e := newEnvWith(t, false, nil, func(c *Config) { c.Thumbnail = fakeQuickLook(t, &got) })
	doc := filepath.Join(e.home, "proj", "report.docx")
	write(t, doc, "PK fake docx")
	resp, err := e.client.Get(e.base + "/api/quicklook?path=" + url.QueryEscape(doc))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType || len(got) != 0 {
		t.Errorf("status = %d, handed over %d; want 415 and nothing drawn", resp.StatusCode, len(got))
	}
}
