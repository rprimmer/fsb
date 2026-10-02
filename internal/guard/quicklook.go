package guard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// quickLookTypes are the document types whose picture fsb asks macOS Quick
// Look to draw: iWork and Microsoft Office documents, which fsb cannot show
// itself. iWork documents may be single files or packages (folders).
var quickLookTypes = map[string]bool{
	".numbers": true, ".pages": true, ".key": true,
	".docx": true, ".doc": true, ".xlsx": true, ".xls": true, ".pptx": true, ".ppt": true,
}

// maxPackageEntries bounds the walk of a package document.
const maxPackageEntries = 5000

// ErrChanged reports that a file changed identity while it was being read
// by another program.
var ErrChanged = errors.New("changed while being read")

// QuickLookTarget decides whether the document at p may be handed to Quick
// Look, by path, for a picture of it. It applies every check Open does, and
// returns the real path to hand over plus a function that reports whether that
// path still names the same document, to be called once Quick Look is done: it
// opens the document again by path, so a swap in between must void the result.
//
// The document must have one of the quickLookTypes extensions and must be on
// disk: a cloud-only document, or a package with any cloud-only part, is
// refused, since Quick Look would download it. A package is refused if
// anything inside it is a symbolic link (Quick Look would follow it), is
// denied by the rules, or is a hard link to a denied file.
func (g *Guard) QuickLookTarget(p string) (string, func() bool, error) {
	f, fi, real, err := g.open(p)
	if err != nil {
		return "", nil, err
	}
	f.Close()
	if !quickLookTypes[strings.ToLower(filepath.Ext(real))] {
		return "", nil, ErrUnsupported
	}
	if err := refuseDataless(fi); err != nil {
		return "", nil, err
	}
	if fi.IsDir() {
		if err := g.checkPackage(real); err != nil {
			return "", nil, err
		}
	} else if !fi.Mode().IsRegular() {
		return "", nil, ErrUnsupported
	}
	same := func() bool {
		f2, fi2, real2, err := g.open(p)
		if err != nil {
			return false
		}
		f2.Close()
		return real2 == real && os.SameFile(fi, fi2) && fi2.ModTime().Equal(fi.ModTime()) && fi2.Size() == fi.Size()
	}
	return real, same, nil
}

// checkPackage walks a package document without following links.
func (g *Guard) checkPackage(root string) error {
	n := 0
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return mapErr(err)
		}
		if n++; n > maxPackageEntries {
			return ErrUnsupported
		}
		if path == root {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !(d.IsDir() || d.Type().IsRegular()) {
			return ErrUnsupported
		}
		if r := g.deny.Match(path, d.IsDir()); r.Matched {
			return &DeniedError{Path: path, Rule: r.Rule}
		}
		fi, err := d.Info()
		if err != nil {
			return mapErr(err)
		}
		if g.hardLinkToDenied(fi) {
			return &DeniedError{Path: path, Rule: "hard link to a denied file"}
		}
		return refuseDataless(fi)
	})
}
