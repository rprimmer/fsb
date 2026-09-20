package guard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

const (
	maxXattrs     = 64
	maxXattrValue = 4096
)

// XAttr is one extended attribute. Value is omitted when values were not
// requested or the attribute is larger than maxXattrValue (Size still tells).
type XAttr struct {
	Name     string `json:"name"`
	Size     int    `json:"size,omitempty"`
	Value    string `json:"value,omitempty"`
	Encoding string `json:"encoding,omitempty"` // "utf8" or "hex"
	Large    bool   `json:"large,omitempty"`
}

// Meta describes one entry.
type Meta struct {
	Name          string      `json:"name"`
	Path          string      `json:"path"`
	IsDir         bool        `json:"isDir"`
	Size          int64       `json:"size"`
	ModTime       time.Time   `json:"modTime"`
	Mode          fs.FileMode `json:"mode"`
	IsSymlink     bool        `json:"isSymlink,omitempty"`
	SymlinkTarget string      `json:"symlinkTarget,omitempty"`
	Dataless      bool        `json:"dataless,omitempty"`
	XAttrs        []XAttr     `json:"xattrs"`
}

// Meta returns details and extended attributes for an entry. Attributes are
// read through the descriptor Open verified, so they belong to the same file
// whose path passed the deny rules. With values false only names are listed,
// which is what the optional listing column uses.
func (g *Guard) Meta(p string, values bool) (Meta, error) {
	f, fi, err := g.Open(p)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()

	clean := filepath.Clean(p)
	m := Meta{
		Name:     filepath.Base(clean),
		Path:     clean,
		IsDir:    fi.IsDir(),
		Size:     fi.Size(),
		ModTime:  fi.ModTime(),
		Mode:     fi.Mode(),
		Dataless: isDataless(fi),
		XAttrs:   []XAttr{},
	}
	if li, err := os.Lstat(clean); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		m.IsSymlink = true
		// Open already confirmed the final target is allowed.
		if t, err := os.Readlink(clean); err == nil {
			m.SymlinkTarget = t
		}
	}

	names, err := listXattr(int(f.Fd()))
	if err != nil {
		return m, nil // attributes are a nicety; the rest is still useful
	}
	for i, name := range names {
		if i >= maxXattrs {
			break
		}
		x := XAttr{Name: name}
		if values {
			val, size, err := getXattr(int(f.Fd()), name, maxXattrValue)
			if err == nil {
				x.Size = size
				if val == nil && size > 0 {
					x.Large = true
				} else if size > 0 {
					x.Value, x.Encoding = encodeXattr(val)
				}
			}
		}
		m.XAttrs = append(m.XAttrs, x)
	}
	return m, nil
}

// encodeXattr renders a value as text when it is printable UTF-8 and as
// space-separated hex otherwise (as `xattr -l` does).
func encodeXattr(b []byte) (string, string) {
	if utf8.Valid(b) {
		printable := true
		for _, c := range b {
			if (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
				printable = false
				break
			}
		}
		if printable {
			return string(b), "utf8"
		}
	}
	return fmt.Sprintf("% x", b), "hex"
}
