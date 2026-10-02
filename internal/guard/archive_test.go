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
	"time"
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

// makeEmptyZip writes a valid zip of n empty stored entries with Go's own
// writer, which switches to ZIP64 above 65,535 entries.
func makeEmptyZip(t testing.TB, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range n {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: itoa(i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The entry limit must hold for valid archives, including ZIP64 ones whose
// 16-bit count field says nothing (it reads 0xFFFF), not only for archives
// that lie about their count.
func TestZipEntryLimitHoldsForValidArchives(t *testing.T) {
	fx := newFixture(t)
	for _, n := range []int{70_000, archiveMaxScanned, archiveMaxScanned + 1} {
		p := filepath.Join(fx.home, "arch", "n"+itoa(n)+".zip")
		put(t, p, makeEmptyZip(t, n))
		l, err := fx.g.ListArchive(p)
		if err != nil {
			t.Fatalf("%d entries: %v", n, err)
		}
		wantTotal, wantIncomplete := n, false
		if n > archiveMaxScanned {
			wantTotal, wantIncomplete = archiveMaxScanned, true
		}
		if l.Total != wantTotal || l.Incomplete != wantIncomplete || len(l.Entries) != archiveMaxShown || !l.Truncated {
			t.Errorf("%d entries: total=%d incomplete=%v shown=%d truncated=%v; want total=%d incomplete=%v",
				n, l.Total, l.Incomplete, len(l.Entries), l.Truncated, wantTotal, wantIncomplete)
		}
	}
}

// The central directory is read within a byte budget too, so huge names or
// extra fields cannot make it cost more than that.
func TestZipDirectoryByteBudget(t *testing.T) {
	old := archiveMaxZipDir
	archiveMaxZipDir = 4096
	defer func() { archiveMaxZipDir = old }()
	fx := newFixture(t)
	names := make([]string, 20)
	for i := range names {
		names[i] = strings.Repeat("n", 1000) + itoa(i)
	}
	p := filepath.Join(fx.home, "arch", "long.zip")
	put(t, p, makeZip(t, names...))
	l, err := fx.g.ListArchive(p)
	if err != nil || !l.Incomplete || l.Total == 0 || l.Total >= 20 {
		t.Fatalf("%+v %v", l.Total, err)
	}
}

// A count that disagrees with the directory is refused rather than trusted.
func TestZipWithAMisleadingCountIsRefused(t *testing.T) {
	fx := newFixture(t)
	z := makeZip(t, "a", "b", "c")
	i := bytes.LastIndex(z, []byte("PK\x05\x06"))
	z[i+8], z[i+10] = 1, 1 // claims one entry; the directory holds three
	p := filepath.Join(fx.home, "arch", "lies.zip")
	put(t, p, z)
	if _, err := fx.g.ListArchive(p); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// Self-extracting archives (a program with a zip appended) still list.
func TestZipWithDataInFrontStillLists(t *testing.T) {
	fx := newFixture(t)
	p := filepath.Join(fx.home, "arch", "sfx.zip")
	put(t, p, append([]byte("PK\x03\x04"+strings.Repeat("stub", 100)), makeZip(t, "a", "b")...))
	l, err := fx.g.ListArchive(p)
	if err != nil || l.Total != 2 || l.Entries[1].Name != "b" {
		t.Fatalf("%+v %v", l, err)
	}
}

// A name is shown, never used, so a very long one is cut for display.
func TestArchiveNamesAreCutForDisplay(t *testing.T) {
	fx := newFixture(t)
	p := filepath.Join(fx.home, "arch", "longname.zip")
	put(t, p, makeZip(t, strings.Repeat("é", 30_000)))
	l, err := fx.g.ListArchive(p)
	if err != nil || len(l.Entries) != 1 {
		t.Fatal(l, err)
	}
	if n := len(l.Entries[0].Name); n > archiveMaxName+len("…") {
		t.Errorf("name is %d bytes", n)
	}
	if !strings.HasSuffix(l.Entries[0].Name, "é…") {
		t.Errorf("cut mid-character or unmarked: %q", l.Entries[0].Name[len(l.Entries[0].Name)-8:])
	}
}

// Modification times come through as the zip records them.
func TestZipModTime(t *testing.T) {
	fx := newFixture(t)
	when := time.Date(2024, 5, 6, 7, 8, 10, 0, time.UTC)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.CreateHeader(&zip.FileHeader{Name: "a", Modified: when})
	zw.Close()
	p := filepath.Join(fx.home, "arch", "time.zip")
	put(t, p, buf.Bytes())
	l, err := fx.g.ListArchive(p)
	if err != nil || !l.Entries[0].ModTime.Equal(when) {
		t.Fatalf("%v %v, want %v", l.Entries, err, when)
	}
}
