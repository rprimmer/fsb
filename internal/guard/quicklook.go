package guard

import (
	"errors"
	"io"
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

// Bounds on what one Quick Look copy may cost.
const (
	maxPackageEntries   = 5000
	MaxQuickLookBytes   = 256 << 20
	quickLookCopyPrefix = "document"
)

// CopyForQuickLook copies the document at p into dir, which must be a fresh
// private folder, and returns the path of the copy, for Quick Look to draw.
//
// Quick Look is never given the user's own path, because it opens what it is
// given by itself and resolves more than the guard can see: a Finder alias
// named report.docx is a small regular file that Quick Look follows to its
// target, which may be denied or outside the roots. A copy holds only bytes
// that the guard itself read, through descriptors it verified, without
// extended attributes or resource forks, so there is nothing left to follow,
// and nothing can be swapped in between the check and the drawing.
//
// The document must have one of the quickLookTypes extensions, must pass every
// check of Open, and must be on disk (a cloud-only document, or a package with
// a cloud-only part, is refused, since reading it would download it). In a
// package (a folder), every file is opened through the guard as well; a part
// that is denied, a symbolic link, a hard link to a denied file, outside the
// package or not a regular file is left out of the copy, exactly as a listing
// leaves it out, so the answer does not reveal it. A document larger than
// MaxQuickLookBytes, or a package with more than maxPackageEntries entries, is
// refused as unsupported.
func (g *Guard) CopyForQuickLook(p, dir string) (string, error) {
	f, fi, real, err := g.open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(real))
	if !quickLookTypes[ext] {
		return "", ErrUnsupported
	}
	if err := refuseDataless(fi); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, quickLookCopyPrefix+ext)
	budget := int64(MaxQuickLookBytes)
	if !fi.IsDir() {
		if !fi.Mode().IsRegular() {
			return "", ErrUnsupported
		}
		return dst, copyBounded(f, dst, &budget)
	}
	return dst, g.copyPackage(real, dst, &budget)
}

// copyPackage copies the visible files of the package at root into dst.
func (g *Guard) copyPackage(root, dst string, budget *int64) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	n := 0
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return mapErr(err)
		}
		if path == root {
			return nil
		}
		if n++; n > maxPackageEntries {
			return ErrUnsupported
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return ErrUnsupported
		}
		switch {
		case d.IsDir():
			if r := g.deny.Match(path, true); r.Matched {
				return filepath.SkipDir
			}
			// Checked before WalkDir reads the folder: a cloud-only part
			// refuses the whole package, as a cloud-only file in it does.
			info, err := d.Info()
			if err != nil {
				return mapErr(err)
			}
			if err := refuseDataless(info); err != nil {
				return err
			}
			return os.Mkdir(filepath.Join(dst, rel), 0o700)
		case !d.Type().IsRegular():
			return nil // a symbolic link, socket or device: left out
		}
		// Opened through the guard, by its full path, so deny rules, roots and
		// hard links are decided on what is actually opened, even if a folder
		// on the way was swapped for a link after the walk listed it.
		f, fi, real, err := g.open(path)
		var denied *DeniedError
		switch {
		case errors.As(err, &denied), errors.Is(err, ErrNotFound), errors.Is(err, ErrNotRegular), errors.Is(err, ErrPermission):
			return nil // left out, as a listing leaves it out
		case err != nil:
			return err
		}
		defer f.Close()
		if !strings.HasPrefix(real, root+"/") || !fi.Mode().IsRegular() {
			return nil
		}
		if err := refuseDataless(fi); err != nil {
			return err
		}
		return copyBounded(f, filepath.Join(dst, rel), budget)
	})
}

// copyBounded copies src into a new file at dst, charging budget, and refuses
// once the budget is exhausted.
func copyBounded(src io.Reader, dst string, budget *int64) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(src, *budget+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return mapErr(err)
	}
	if *budget -= n; *budget < 0 {
		return ErrUnsupported
	}
	return nil
}
