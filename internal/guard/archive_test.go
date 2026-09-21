package guard

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, names ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, _ := zw.Create(n)
		w.Write([]byte("hello"))
	}
	zw.Close()
	return buf.Bytes()
}

func makeTar(t *testing.T, names ...string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, n := range names {
		typ := byte(tar.TypeReg)
		body := "hello"
		if strings.HasSuffix(n, "/") {
			typ, body = tar.TypeDir, ""
		}
		tw.WriteHeader(&tar.Header{Name: n, Size: int64(len(body)), Typeflag: typ, Mode: 0o644})
		tw.Write([]byte(body))
	}
	tw.Close()
	return buf.Bytes()
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func TestListArchiveByContentNotName(t *testing.T) {
	fx := newFixture(t)
	for _, tc := range []struct {
		file, format string
		data         []byte
	}{
		{"a.bin", "zip", makeZip(t, "dir/", "dir/a.txt", "b.txt")},
		{"b.dat", "tar", makeTar(t, "dir/", "dir/a.txt", "b.txt")},
		{"c.thing", "tar.gz", gz(makeTar(t, "dir/", "dir/a.txt", "b.txt"))},
	} {
		p := filepath.Join(fx.home, "arch", tc.file)
		put(t, p, tc.data)
		l, err := fx.g.ListArchive(p)
		if err != nil || l.Format != tc.format || l.Total != 3 || len(l.Entries) != 3 {
			t.Fatalf("%s: %+v %v", tc.file, l, err)
		}
		if !l.Entries[0].IsDir || l.Entries[1].Name != "dir/a.txt" || l.Entries[1].Size != 5 {
			t.Errorf("%s: entries = %+v", tc.file, l.Entries)
		}
	}
}

func TestListArchiveRefusesNonArchives(t *testing.T) {
	fx := newFixture(t)
	for name, data := range map[string][]byte{
		"fake.zip":   []byte("<html><script>x</script></html>"),
		"empty.zip":  {},
		"trunc.zip":  makeZip(t, "a")[:20],
		"plain.gz":   gz([]byte("just text, not a tar archive at all......")),
		"badgz.tgz":  {0x1f, 0x8b, 1, 2, 3},
		"png.tar":    pngHead,
		"garbage.gz": append([]byte{0x1f, 0x8b, 8, 0}, bytes.Repeat([]byte{0xff}, 100)...),
	} {
		p := filepath.Join(fx.home, "arch", name)
		put(t, p, data)
		if _, err := fx.g.ListArchive(p); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	if _, err := fx.g.ListArchive(filepath.Join(fx.home, "arch")); err == nil {
		t.Error("a directory was accepted")
	}
}

func TestListArchiveNamesAreDefanged(t *testing.T) {
	fx := newFixture(t)
	p := filepath.Join(fx.home, "arch", "evil.zip")
	put(t, p, makeZip(t, "../../etc/passwd", "a\x1b[31mred\nline.txt", "bad\xffutf8", "‮RTL.txt"))
	l, err := fx.g.ListArchive(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		for _, r := range e.Name {
			if r < 0x20 || r == 0x7f {
				t.Errorf("control character survived in %q", e.Name)
			}
		}
	}
	if l.Entries[0].Name != "../../etc/passwd" {
		t.Errorf("names are displayed as they are, never used as paths: %q", l.Entries[0].Name)
	}
}

func TestListArchiveIsBounded(t *testing.T) {
	fx := newFixture(t)
	names := make([]string, 3000)
	for i := range names {
		names[i] = "f" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "/" + itoa(i)
	}
	p := filepath.Join(fx.home, "arch", "many.tar.gz")
	put(t, p, gz(makeTar(t, names...)))
	l, err := fx.g.ListArchive(p)
	if err != nil || len(l.Entries) != archiveMaxShown || l.Total != 3000 || !l.Truncated {
		t.Fatalf("shown=%d total=%d truncated=%v err=%v", len(l.Entries), l.Total, l.Truncated, err)
	}

	// A zip whose directory claims more entries than we will index is refused.
	z := makeZip(t, "a")
	i := bytes.LastIndex(z, []byte("PK\x05\x06"))
	z[i+10], z[i+11] = 0xfe, 0xff
	bp := filepath.Join(fx.home, "arch", "huge.zip")
	put(t, bp, z)
	if _, err := fx.g.ListArchive(bp); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported for a claimed 65k-entry zip", err)
	}
}

func itoa(i int) string {
	const d = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{d[i%10]}, b...)
	}
	return string(b)
}

func TestListArchiveStopsAtTheDecompressionLimit(t *testing.T) {
	old := archiveMaxDecompress
	archiveMaxDecompress = 8 << 20
	defer func() { archiveMaxDecompress = old }()
	fx := newFixture(t)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "small.txt", Size: 1, Typeflag: tar.TypeReg, Mode: 0o644})
	tw.Write([]byte("x"))
	const big = 32 << 20
	tw.WriteHeader(&tar.Header{Name: "bomb.bin", Size: big, Typeflag: tar.TypeReg, Mode: 0o644})
	zeros := make([]byte, 1<<20)
	for i := 0; i < big>>20; i++ {
		tw.Write(zeros)
	}
	tw.Close()
	gw.Close()
	p := filepath.Join(fx.home, "arch", "bomb.tgz")
	put(t, p, buf.Bytes())
	l, err := fx.g.ListArchive(p)
	if err != nil || !l.Incomplete || l.Total < 1 {
		t.Fatalf("%+v %v", l, err)
	}
}
