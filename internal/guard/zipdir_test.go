package guard

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

// listZip is a parser of untrusted bytes. It must never panic, and wherever
// archive/zip accepts an archive, the two must agree on what is in it.
func FuzzListZipAgreesWithArchiveZip(f *testing.F) {
	f.Add(makeZip(nil, "dir/", "dir/a.txt", "b.txt"))
	f.Add(makeZip(nil))
	f.Add(append([]byte("stub"), makeZip(nil, "a")...))
	f.Add(makeEmptyZip(f, 3))
	for _, p := range []string{"testdata/infozip-stream.zip", "testdata/infozip-stream-dir.zip"} {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := listZip(bytes.NewReader(b), int64(len(b)))
		zr, zerr := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil || zerr != nil || got.Incomplete {
			return // only compare where both read the whole directory
		}
		if got.Total != len(zr.File) {
			t.Fatalf("total %d, archive/zip %d", got.Total, len(zr.File))
		}
		for i, e := range got.Entries {
			zf := zr.File[i]
			if e.Name != cleanName(zf.Name) || uint64(e.Size) != min(zf.UncompressedSize64, 1<<62) {
				t.Fatalf("entry %d: %+v, archive/zip %q %d", i, e, zf.Name, zf.UncompressedSize64)
			}
			// A zero MS-DOS date is shown as no time; archive/zip makes it 1979-11-30.
			if !e.ModTime.Equal(zf.Modified) && !(e.ModTime.IsZero() && zf.Modified.Year() < 1980) {
				t.Fatalf("entry %d time %v, archive/zip %v", i, e.ModTime, zf.Modified)
			}
		}
	})
}
