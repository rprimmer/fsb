package guard

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits on listing an archive. Listing never extracts anything and never
// writes; the limits keep a hostile archive (a zip bomb, a million tiny
// entries, a gzip that expands enormously) from costing more than a bounded
// amount of memory and time.
const (
	archiveMaxShown   = 1000
	archiveMaxScanned = 200_000
)

// archiveMaxDecompress bounds the bytes of a compressed tar we will stream
// through. A variable only so a test can lower it.
var archiveMaxDecompress int64 = 512 << 20

// archiveMaxZipDir bounds the bytes of a zip central directory we will read.
// A variable only so a test can lower it.
var archiveMaxZipDir int64 = 64 << 20

// archiveMaxName bounds the bytes of a member name kept for display.
const archiveMaxName = 4096

// ArchiveEntry is one member of an archive.
type ArchiveEntry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"isDir"`
	ModTime time.Time `json:"modTime,omitempty"`
}

// ArchiveListing is the table of contents of an archive.
type ArchiveListing struct {
	Format  string         `json:"format"` // "zip", "tar" or "tar.gz"
	Entries []ArchiveEntry `json:"entries"`
	// Total is the number of entries seen. It is a lower bound when Incomplete.
	Total int `json:"total"`
	// Truncated means more entries exist than Entries holds.
	Truncated bool `json:"truncated,omitempty"`
	// Incomplete means listing stopped at a safety limit, so Total may be short.
	Incomplete bool `json:"incomplete,omitempty"`
}

// ListArchive lists the contents of a zip, tar or gzip-compressed tar file
// without extracting it. The format is decided from the file's bytes, never its
// name; anything else is ErrUnsupported. A cloud-only file is never read.
// Member names are untrusted: control characters are replaced, and they are
// only ever displayed, never used as paths.
func (g *Guard) ListArchive(p string) (ArchiveListing, error) {
	f, fi, err := g.Open(p)
	if err != nil {
		return ArchiveListing{}, err
	}
	defer f.Close()
	if fi.IsDir() {
		return ArchiveListing{}, ErrIsDir
	}
	if isDataless(fi) {
		return ArchiveListing{}, ErrDataless
	}
	var head [512]byte
	n, _ := io.ReadFull(f, head[:])
	b := head[:n]
	fail := func(err error) (ArchiveListing, error) { return ArchiveListing{}, mapErr(err) }

	switch {
	case bytes.HasPrefix(b, []byte("PK\x03\x04")) || bytes.HasPrefix(b, []byte("PK\x05\x06")):
		return listZip(f, fi.Size())

	case len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b:
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fail(err)
		}
		zr, err := gzip.NewReader(bufio.NewReader(f))
		if err != nil {
			return ArchiveListing{}, ErrUnsupported
		}
		defer zr.Close()
		return listTar(&io.LimitedReader{R: zr, N: archiveMaxDecompress}, "tar.gz")

	case n >= 262 && string(b[257:262]) == "ustar":
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fail(err)
		}
		return listTar(f, "tar")
	}
	return ArchiveListing{}, ErrUnsupported
}

func listTar(r io.Reader, format string) (ArchiveListing, error) {
	l := ArchiveListing{Format: format}
	tr := tar.NewReader(r)
	lim, _ := r.(*io.LimitedReader)
	for {
		h, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A limit hit while streaming, or a damaged tail: report what we have.
			if l.Total == 0 {
				return ArchiveListing{}, ErrUnsupported
			}
			l.Incomplete = true
			break
		}
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		}
		l.Total++
		if len(l.Entries) < archiveMaxShown {
			l.Entries = append(l.Entries, ArchiveEntry{
				Name:    cleanName(h.Name),
				Size:    h.Size,
				IsDir:   h.Typeflag == tar.TypeDir,
				ModTime: h.ModTime,
			})
		} else {
			l.Truncated = true
		}
		if l.Total >= archiveMaxScanned || (lim != nil && lim.N <= 0) {
			l.Incomplete = true
			break
		}
	}
	if l.Total == 0 {
		return ArchiveListing{}, ErrUnsupported
	}
	return l, nil
}

// cleanName makes an archive member name safe to show: control characters and
// invalid UTF-8 become U+FFFD, and a name longer than archiveMaxName bytes is cut
// (at a character boundary) and ends in "…".
func cleanName(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) > archiveMaxName {
		cut := archiveMaxName
		for !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 {
			return '�'
		}
		return r
	}, s)
}
