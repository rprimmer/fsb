package guard

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

// Head kinds.
const (
	KindText     = "text"
	KindBinary   = "binary"
	KindEmpty    = "empty"
	KindDataless = "dataless"
)

var (
	// ErrIsDir is returned when a file operation is asked of a directory.
	ErrIsDir = errors.New("is a directory")
	// ErrDataless is returned for cloud-only files, which fsb never reads
	// implicitly because reading them triggers a download.
	ErrDataless = errors.New("file is not downloaded")
	// ErrUnsupported is returned when a file is not of a type that may be
	// served inline.
	ErrUnsupported = errors.New("unsupported file type")
)

// Head is a bounded, classified look at the start of a file.
type Head struct {
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Head reads at most max bytes from the start of a file and classifies it. The
// client only ever receives text that is valid UTF-8; binary files report their
// kind and size and nothing else. A cloud-only file is never read.
func (g *Guard) Head(p string, max int) (Head, error) {
	if max < 1 {
		max = 1
	}
	f, fi, err := g.Open(p)
	if err != nil {
		return Head{}, err
	}
	defer f.Close()
	if fi.IsDir() {
		return Head{}, ErrIsDir
	}

	h := Head{Size: fi.Size()}
	switch {
	case isDataless(fi):
		h.Kind = KindDataless // reading it would download it from the cloud
		return h, nil
	case fi.Size() == 0:
		h.Kind = KindEmpty
		return h, nil
	}

	buf := make([]byte, max)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Head{}, mapErr(err)
	}
	h.Truncated = int64(n) < fi.Size()
	text, ok := asText(buf[:n], h.Truncated)
	if !ok {
		h.Kind = KindBinary
		return h, nil
	}
	h.Kind = KindText
	h.Text = text
	return h, nil
}

// asText reports whether b looks like UTF-8 text and returns it. truncated says
// b was cut off, so a multi-byte character split at the end is not an error.
func asText(b []byte, truncated bool) (string, bool) {
	if len(b) >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF)) {
		return "", false // UTF-16: not shown
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if bytes.IndexByte(b, 0) >= 0 {
		return "", false
	}
	if truncated {
		b = trimPartialRune(b)
	}
	if !utf8.Valid(b) {
		return "", false
	}
	ctrl := 0
	for _, c := range b {
		if (c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f') || c == 0x7f {
			ctrl++
		}
	}
	if ctrl*3 > len(b) {
		return "", false // over a third control characters: binary (ANSI-colored logs stay text)
	}
	return string(b), true
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			break
		}
	}
	return b
}
