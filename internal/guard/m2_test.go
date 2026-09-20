package guard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func put(t testing.TB, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- Head ------------------------------------------------------------------

func TestHead(t *testing.T) {
	fx := newFixture(t)
	dir := filepath.Join(fx.home, "heads")
	cases := []struct {
		name      string
		content   []byte
		max       int
		kind      string
		text      string
		truncated bool
	}{
		{"plain text", []byte("hello world\n"), 100, KindText, "hello world\n", false},
		{"empty", nil, 100, KindEmpty, "", false},
		{"utf-8 bom is dropped", []byte("\xef\xbb\xbfhello"), 100, KindText, "hello", false},
		{"cut mid-rune stays valid", []byte(strings.Repeat("é", 10)), 5, KindText, "éé", true},
		{"cut mid 3-byte rune", []byte(strings.Repeat("€", 4)), 4, KindText, "€", true},
		{"max bounds the text", []byte(strings.Repeat("a", 100)), 4, KindText, "aaaa", true},
		{"nul byte is binary", []byte("ab\x00cd"), 100, KindBinary, "", false},
		{"invalid utf-8 is binary", []byte("\xc3\x28 not utf8"), 100, KindBinary, "", false},
		{"utf-16 is not shown", []byte("\xff\xfeh\x00i\x00"), 100, KindBinary, "", false},
		{"mostly control chars is binary", bytes.Repeat([]byte{1}, 40), 100, KindBinary, "", false},
		{"ansi colored log stays text", []byte("\x1b[31mERROR\x1b[0m something failed here\n"), 200, KindText, "\x1b[31mERROR\x1b[0m something failed here\n", false},
		{"png header is binary", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 100, KindBinary, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_"))
			put(t, p, tc.content)
			h, err := fx.g.Head(p, tc.max)
			if err != nil {
				t.Fatal(err)
			}
			if h.Kind != tc.kind || h.Text != tc.text || h.Truncated != tc.truncated {
				t.Fatalf("got kind=%q text=%q truncated=%v, want kind=%q text=%q truncated=%v", h.Kind, h.Text, h.Truncated, tc.kind, tc.text, tc.truncated)
			}
			if h.Size != int64(len(tc.content)) {
				t.Errorf("size = %d, want %d", h.Size, len(tc.content))
			}
			if h.Kind != KindText && h.Text != "" {
				t.Errorf("non-text kinds must never carry text, got %q", h.Text)
			}
		})
	}
}

func TestHeadErrors(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.g.Head(filepath.Join(fx.home, "proj"), 100); !errors.Is(err, ErrIsDir) {
		t.Errorf("directory: %v", err)
	}
	if _, err := fx.g.Head(filepath.Join(fx.home, ".ssh", "id_ed25519"), 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("denied: %v", err)
	}
	if _, err := fx.g.Head(filepath.Join(fx.home, "proj", "shortcut", "id_ed25519"), 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("via symlink into a denied dir: %v", err)
	}
	if _, err := fx.g.Head(filepath.Join(fx.home, "nope"), 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if h, err := fx.g.Head(filepath.Join(fx.home, "proj", "hello.txt"), 0); err != nil || len(h.Text) != 1 {
		t.Errorf("max < 1 is clamped to 1: %+v %v", h, err)
	}
}

func TestHiddenFilesAreStillPeekable(t *testing.T) {
	fx := newFixture(t)
	// Hide rules are cosmetic; only deny blocks reading.
	h, err := fx.g.Head(filepath.Join(fx.home, "node_modules", "pkg", "index.js"), 100)
	if err != nil || h.Kind != KindText || h.Text != "js" {
		t.Fatalf("%+v %v", h, err)
	}
}

// ---- Meta / extended attributes -------------------------------------------

func setX(t *testing.T, path, name string, val []byte) {
	t.Helper()
	if err := unix.Setxattr(path, name, val, 0); err != nil {
		t.Skipf("extended attributes are not supported here: %v", err)
	}
}

func TestMetaXattrs(t *testing.T) {
	fx := newFixture(t)
	p := filepath.Join(fx.home, "proj", "hello.txt")
	setX(t, p, "user.fsb.text", []byte("hi there"))
	setX(t, p, "user.fsb.bin", []byte{0x00, 0x01, 0xfe, 0xff})
	setX(t, p, "user.fsb.big", bytes.Repeat([]byte("x"), maxXattrValue+1))

	m, err := fx.g.Meta(p, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]XAttr{}
	for _, x := range m.XAttrs {
		got[x.Name] = x
	}
	if x := got["user.fsb.text"]; x.Value != "hi there" || x.Encoding != "utf8" || x.Size != 8 {
		t.Errorf("text attribute: %+v", x)
	}
	if x := got["user.fsb.bin"]; x.Value != "00 01 fe ff" || x.Encoding != "hex" {
		t.Errorf("binary attribute: %+v", x)
	}
	if x := got["user.fsb.big"]; !x.Large || x.Value != "" || x.Size != maxXattrValue+1 {
		t.Errorf("oversized attribute must report its size but no value: %+v", x)
	}

	names, err := fx.g.Meta(p, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range names.XAttrs {
		if x.Value != "" || x.Size != 0 {
			t.Errorf("names-only mode must not read values: %+v", x)
		}
	}
	if len(names.XAttrs) < 3 {
		t.Errorf("names-only mode lost attributes: %+v", names.XAttrs)
	}
}

func TestMetaBasics(t *testing.T) {
	fx := newFixture(t)
	m, err := fx.g.Meta(filepath.Join(fx.home, "proj", "hello.txt"), true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "hello.txt" || m.Size != 5 || m.IsDir || m.IsSymlink || m.XAttrs == nil {
		t.Errorf("%+v", m)
	}

	link := filepath.Join(fx.home, "hello-link")
	m, err = fx.g.Meta(link, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsSymlink || m.SymlinkTarget != filepath.Join(fx.home, "proj", "hello.txt") || m.Size != 5 {
		t.Errorf("symlink meta: %+v", m)
	}

	d, err := fx.g.Meta(filepath.Join(fx.home, "proj"), true)
	if err != nil || !d.IsDir {
		t.Errorf("directory meta: %+v %v", d, err)
	}
}

func TestMetaRespectsDeny(t *testing.T) {
	fx := newFixture(t)
	for _, p := range []string{
		filepath.Join(fx.home, ".ssh"),
		filepath.Join(fx.home, ".ssh", "id_ed25519"),
		filepath.Join(fx.home, "proj", "shortcut", "id_ed25519"),
		filepath.Join(fx.home, "escape", "secret.txt"),
	} {
		if _, err := fx.g.Meta(p, true); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestEncodeXattr(t *testing.T) {
	for _, tc := range []struct {
		in       []byte
		val, enc string
	}{
		{[]byte("plain"), "plain", "utf8"},
		{[]byte("two\nlines\tok"), "two\nlines\tok", "utf8"},
		{[]byte("nul\x00in"), "6e 75 6c 00 69 6e", "hex"},
		{[]byte{0xff, 0xfe}, "ff fe", "hex"},
		{[]byte("bell\x07"), "62 65 6c 6c 07", "hex"},
	} {
		if v, e := encodeXattr(tc.in); v != tc.val || e != tc.enc {
			t.Errorf("encodeXattr(%q) = %q,%q want %q,%q", tc.in, v, e, tc.val, tc.enc)
		}
	}
}

// ---- Images ----------------------------------------------------------------

var (
	pngHead  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR....")
	jpegHead = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01")
	gifHead  = []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00")
	webpHead = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
)

func TestOpenImageAcceptsRasterImagesByContent(t *testing.T) {
	fx := newFixture(t)
	for _, tc := range []struct {
		file string
		data []byte
		ct   string
	}{
		{"a.png", pngHead, "image/png"},
		{"a.jpg", jpegHead, "image/jpeg"},
		{"a.gif", gifHead, "image/gif"},
		{"a.webp", webpHead, "image/webp"},
		{"gif87.bin", []byte("GIF87a........"), "image/gif"},
		{"misnamed.txt", pngHead, "image/png"}, // extension is irrelevant
	} {
		p := filepath.Join(fx.home, "img", tc.file)
		put(t, p, tc.data)
		f, _, ct, err := fx.g.OpenImage(p)
		if err != nil || ct != tc.ct {
			t.Errorf("%s: ct=%q err=%v, want %q", tc.file, ct, err, tc.ct)
			continue
		}
		b := make([]byte, 1)
		if _, err := io.ReadFull(f, b); err != nil || b[0] != tc.data[0] {
			t.Errorf("%s: the file must be rewound to the start after sniffing (%v)", tc.file, err)
		}
		f.Close()
	}
}

func TestOpenImageRefusesEverythingElse(t *testing.T) {
	fx := newFixture(t)
	for _, tc := range []struct {
		file string
		data []byte
	}{
		{"evil.png", []byte("<html><script>alert(1)</script></html>")},
		{"evil2.png", []byte("<!doctype html><script>alert(1)</script>")},
		{"vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)},
		{"vector2.png", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)},
		{"text.png", []byte("just text")},
		{"empty.png", nil},
		{"short.png", []byte("\x89PN")},
		{"riff-not-webp.png", []byte("RIFF\x24\x00\x00\x00WAVEfmt ")},
		{"riff-truncated.png", []byte("RIFF\x24\x00\x00\x00WEB")},
		{"pdf.png", []byte("%PDF-1.7 ...")},
	} {
		p := filepath.Join(fx.home, "img", tc.file)
		put(t, p, tc.data)
		if _, _, _, err := fx.g.OpenImage(p); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s must be refused, err = %v", tc.file, err)
		}
	}
	if _, _, _, err := fx.g.OpenImage(filepath.Join(fx.home, "proj")); !errors.Is(err, ErrIsDir) {
		t.Errorf("directory: %v", err)
	}
}

func TestOpenImageRespectsDeny(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, ".ssh", "photo.png"), pngHead)
	if _, _, _, err := fx.g.OpenImage(filepath.Join(fx.home, ".ssh", "photo.png")); !errors.Is(err, ErrNotFound) {
		t.Errorf("an image inside a denied directory: %v", err)
	}
	put(t, filepath.Join(fx.outside, "photo.png"), pngHead)
	if _, err := os.Stat(filepath.Join(fx.outside, "photo.png")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fx.g.OpenImage(filepath.Join(fx.home, "escape", "photo.png")); !errors.Is(err, ErrNotFound) {
		t.Errorf("an image reached through an escaping symlink: %v", err)
	}
}

func TestSniffImage(t *testing.T) {
	for in, want := range map[string]string{
		"\x89PNG\r\n\x1a\n": "image/png",
		"\xff\xd8\xff":      "image/jpeg",
		"GIF87a":            "image/gif",
		"GIF89a":            "image/gif",
		"RIFFxxxxWEBP":      "image/webp",
		"RIFFxxxxWAVE":      "",
		"RIFFxxxxWEB":       "",
		"":                  "",
		"BM....":            "", // BMP is not on the allowlist
		"<svg":              "",
		"\x00\x00\x01\x00":  "", // ICO
	} {
		if got := sniffImage([]byte(in)); got != want {
			t.Errorf("sniffImage(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- Search ----------------------------------------------------------------

func searchAll(t *testing.T, g *Guard, root, q string, lim SearchLimits) ([]SearchMatch, int, bool, error) {
	t.Helper()
	var out []SearchMatch
	visited, truncated, err := g.Search(context.Background(), root, q, lim, func(m SearchMatch) error {
		out = append(out, m)
		return nil
	})
	return out, visited, truncated, err
}

func rels(ms []SearchMatch) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Rel)
	}
	return out
}

func TestSearchFindsMatchesBreadthFirst(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, "proj", "deep", "a", "b", "Needle-deep.txt"), []byte("x"))
	put(t, filepath.Join(fx.home, "proj", "needle-shallow.txt"), []byte("x"))
	put(t, filepath.Join(fx.home, "proj", "deep", "needle-mid.txt"), []byte("x"))

	ms, _, truncated, err := searchAll(t, fx.g, fx.home, "NEEDLE", DefaultSearchLimits)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	want := []string{"proj/needle-shallow.txt", "proj/deep/needle-mid.txt", "proj/deep/a/b/Needle-deep.txt"}
	if strings.Join(rels(ms), "|") != strings.Join(want, "|") {
		t.Fatalf("results (shallow first) = %v, want %v", rels(ms), want)
	}
	if ms[0].Path != filepath.Join(fx.home, "proj", "needle-shallow.txt") || ms[0].Name != "needle-shallow.txt" || ms[0].IsDir {
		t.Errorf("match fields: %+v", ms[0])
	}
}

func TestSearchNeverReportsOrEntersDeniedOrHidden(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, ".ssh", "needle-key"), []byte("SECRET"))          // denied
	put(t, filepath.Join(fx.home, "node_modules", "pkg", "needle.js"), []byte("x")) // hidden by default ignore rules
	put(t, filepath.Join(fx.home, "proj", "needle-ok.txt"), []byte("x"))
	// A symlink into a denied directory must not be a way around it.
	put(t, filepath.Join(fx.home, ".ssh", "needle-via-link"), []byte("SECRET"))

	ms, _, _, err := searchAll(t, fx.g, fx.home, "needle", DefaultSearchLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := rels(ms); len(got) != 1 || got[0] != "proj/needle-ok.txt" {
		t.Fatalf("results = %v, want only proj/needle-ok.txt", got)
	}
	// A denied name itself is not searchable either.
	ms, _, _, _ = searchAll(t, fx.g, fx.home, ".ssh", DefaultSearchLimits)
	if len(ms) != 0 {
		t.Fatalf("a denied directory's name leaked into search: %v", rels(ms))
	}
	ms, _, _, _ = searchAll(t, fx.g, fx.home, "shortcut", DefaultSearchLimits)
	if len(ms) != 0 {
		t.Fatalf("a symlink to a denied directory leaked into search: %v", rels(ms))
	}
}

func TestSearchDoesNotFollowSymlinkedDirectories(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, "proj", "unique-marker.txt"), []byte("x"))
	// A cycle: proj/loop -> proj. Following it would find the marker forever.
	if err := os.Symlink(filepath.Join(fx.home, "proj"), filepath.Join(fx.home, "proj", "loop")); err != nil {
		t.Fatal(err)
	}
	ms, visited, truncated, err := searchAll(t, fx.g, fx.home, "unique-marker", DefaultSearchLimits)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if len(ms) != 1 || visited > 100 {
		t.Fatalf("results=%v visited=%d; the symlinked directory must not be searched again", rels(ms), visited)
	}
}

func TestSearchLimitsAndCancellation(t *testing.T) {
	fx := newFixture(t)
	for i := 0; i < 30; i++ {
		put(t, filepath.Join(fx.home, "many", "hit-"+string(rune('a'+i%26))+string(rune('a'+i/26))), []byte("x"))
	}
	ms, _, truncated, err := searchAll(t, fx.g, fx.home, "hit-", SearchLimits{MaxResults: 5, MaxVisited: 1_000_000})
	if err != nil || !truncated || len(ms) != 5 {
		t.Fatalf("result limit: n=%d truncated=%v err=%v", len(ms), truncated, err)
	}
	_, visited, truncated, err := searchAll(t, fx.g, fx.home, "zzz-no-match", SearchLimits{MaxResults: 10, MaxVisited: 10})
	if err != nil || !truncated || visited > 10 {
		t.Fatalf("visit limit: visited=%d truncated=%v err=%v", visited, truncated, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := fx.g.Search(ctx, fx.home, "hit-", DefaultSearchLimits, func(SearchMatch) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context must stop the search, got %v", err)
	}
	boom := errors.New("client went away")
	_, _, err = fx.g.Search(context.Background(), fx.home, "hit-", DefaultSearchLimits, func(SearchMatch) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("a callback error must stop the search, got %v", err)
	}
}

func TestSearchFoldsCaseAndUnicode(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, "proj", "Café Menu.txt"), []byte("x")) // NFC on disk
	for _, q := range []string{"café", "CAFÉ", "café", "  menu  "} {
		ms, _, _, err := searchAll(t, fx.g, fx.home, q, DefaultSearchLimits)
		if err != nil || len(ms) != 1 {
			t.Errorf("query %q: results=%v err=%v", q, rels(ms), err)
		}
	}
}

func TestSearchRootErrorsAndEmptyQuery(t *testing.T) {
	fx := newFixture(t)
	for _, root := range []string{
		filepath.Join(fx.home, ".ssh"),
		filepath.Join(fx.home, "proj", "shortcut"),
		fx.outside,
		filepath.Join(fx.home, "escape"),
		filepath.Join(fx.home, "does-not-exist"),
		"relative",
		"",
	} {
		if _, _, _, err := searchAll(t, fx.g, root, "x", DefaultSearchLimits); !errors.Is(err, ErrNotFound) {
			t.Errorf("root %q: err = %v, want ErrNotFound", root, err)
		}
	}
	if _, _, _, err := searchAll(t, fx.g, filepath.Join(fx.home, "proj", "hello.txt"), "x", DefaultSearchLimits); !errors.Is(err, ErrNotDir) {
		t.Errorf("a file as root: %v", err)
	}
	ms, visited, _, err := searchAll(t, fx.g, fx.home, "   ", DefaultSearchLimits)
	if err != nil || len(ms) != 0 || visited != 0 {
		t.Errorf("empty query: %v %d %v", ms, visited, err)
	}
}
