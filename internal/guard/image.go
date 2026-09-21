package guard

import (
	"bytes"
	"io"
	"io/fs"
	"os"
)

// OpenImage opens a file for inline display, but only if its leading bytes
// identify it as a raster image type that is safe to render: PNG, JPEG, GIF or
// WebP. The file extension is never consulted, so an HTML or SVG file renamed
// to .png is refused, and SVG (which can carry script) is never accepted. The
// returned string is the Content-Type to serve.
func (g *Guard) OpenImage(p string) (*os.File, fs.FileInfo, string, error) {
	return g.openSniffed(p, sniffImage)
}

// OpenPDF is OpenImage for PDF documents: the file must begin with "%PDF-".
// The name is never consulted. The returned string is the Content-Type to serve.
func (g *Guard) OpenPDF(p string) (*os.File, fs.FileInfo, string, error) {
	return g.openSniffed(p, func(b []byte) string {
		if bytes.HasPrefix(b, []byte("%PDF-")) {
			return "application/pdf"
		}
		return ""
	})
}

func (g *Guard) openSniffed(p string, sniff func([]byte) string) (*os.File, fs.FileInfo, string, error) {
	f, fi, err := g.Open(p)
	if err != nil {
		return nil, nil, "", err
	}
	fail := func(err error) (*os.File, fs.FileInfo, string, error) {
		f.Close()
		return nil, nil, "", err
	}
	if fi.IsDir() {
		return fail(ErrIsDir)
	}
	if isDataless(fi) {
		return fail(ErrDataless)
	}
	var head [16]byte
	n, err := io.ReadFull(f, head[:])
	if err != nil && n == 0 {
		return fail(ErrUnsupported)
	}
	ct := sniff(head[:n])
	if ct == "" {
		return fail(ErrUnsupported)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(mapErr(err))
	}
	return f, fi, ct, nil
}

func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}
