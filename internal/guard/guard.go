// Package guard is fsb's filesystem chokepoint. Every read of the user's
// filesystem goes through a Guard, which enforces the configured roots and the
// deny rules, and applies hide rules to listings. No other package that serves
// HTTP may touch the filesystem directly (see the architecture test in
// internal/server).
//
// Known limitation: rules are path-based, so a hard link to a denied file that
// lives elsewhere is not detected.
package guard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/rprimmer/fsb/internal/rules"
)

var (
	// ErrNotFound covers nonexistent, malformed, outside-root and denied paths.
	// Callers must not distinguish these when talking to clients.
	ErrNotFound = errors.New("not found")
	// ErrPermission is returned for OS permission errors on paths that passed
	// every rule (so it reveals nothing about denied paths).
	ErrPermission = errors.New("permission denied")
	// ErrNotRegular is returned when opening a socket, device, FIFO, etc.
	ErrNotRegular = errors.New("not a regular file or directory")
	// ErrNotDir is returned by List for a non-directory.
	ErrNotDir = errors.New("not a directory")
)

// DeniedError says why a path was refused. It matches ErrNotFound with
// errors.Is so that a careless caller still answers 404; only debug output
// should ever surface Rule.
type DeniedError struct {
	Path string
	Rule string
}

func (e *DeniedError) Error() string        { return "denied by " + e.Rule }
func (e *DeniedError) Is(target error) bool { return target == ErrNotFound }

// Entry is one item of a directory listing.
type Entry struct {
	Name      string      `json:"name"`
	IsDir     bool        `json:"isDir"`
	IsSymlink bool        `json:"isSymlink,omitempty"`
	Broken    bool        `json:"broken,omitempty"`
	Size      int64       `json:"size"`
	ModTime   time.Time   `json:"modTime"`
	Mode      fs.FileMode `json:"mode"`
}

// Guard enforces roots and rules.
type Guard struct {
	roots []string // real paths
	deny  *rules.Set
	hide  *rules.Set
}

// New creates a Guard. Each root must exist and is resolved to its real path.
func New(roots []string, deny, hide *rules.Set) (*Guard, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one root is required")
	}
	g := &Guard{deny: deny, hide: hide}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("root %q is not absolute", r)
		}
		real, err := filepath.EvalSymlinks(filepath.Clean(r))
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", r, err)
		}
		fi, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", r, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("root %q is not a directory", r)
		}
		g.roots = append(g.roots, real)
	}
	return g, nil
}

// Roots returns the resolved root paths.
func (g *Guard) Roots() []string { return append([]string(nil), g.roots...) }

// fold normalizes a path for case- and normalization-insensitive comparison.
func fold(s string) string { return strings.ToLower(norm.NFC.String(s)) }

// inRoots reports whether real is a root or lies beneath one.
func (g *Guard) inRoots(real string) bool {
	p := fold(real)
	for _, r := range g.roots {
		r = fold(r)
		if r == "/" || p == r || strings.HasPrefix(p, strings.TrimRight(r, "/")+"/") {
			return true
		}
	}
	return false
}

// violation checks a resolved real path against the roots and deny rules.
func (g *Guard) violation(real string, isDir bool) *DeniedError {
	if !g.inRoots(real) {
		return &DeniedError{Path: real, Rule: "outside configured roots"}
	}
	if r := g.deny.Match(real, isDir); r.Matched {
		return &DeniedError{Path: real, Rule: r.Rule}
	}
	return nil
}

// canonical validates and cleans a client-supplied path. URL decoding is the
// HTTP layer's job and happens exactly once; a literal "%2e%2e" here is just a
// file name.
func canonical(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return "", ErrNotFound
	}
	return filepath.Clean(p), nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR),
		errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENAMETOOLONG):
		return ErrNotFound
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return ErrPermission
	}
	return fmt.Errorf("open: %w", err)
}

// Open opens a regular file or directory for reading. The returned file is
// what must be served: rules are verified against the file that is actually
// open, not a path that could change afterwards.
//
// Order of checks: (1) lexical deny before touching the filesystem, (2) open
// without following FIFOs/devices, (3) resolve the real path and confirm it is
// the very file we opened, (4) roots and deny rules on the real path.
func (g *Guard) Open(p string) (*os.File, fs.FileInfo, error) {
	clean, err := canonical(p)
	if err != nil {
		return nil, nil, err
	}
	// Type is unknown before opening, so assume a directory: this can only
	// over-deny a plain file that shares a name with a dir-only rule.
	if r := g.deny.Match(clean, true); r.Matched {
		return nil, nil, &DeniedError{Path: clean, Rule: r.Rule}
	}

	fd, err := syscall.Open(clean, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	// Reject FIFOs, sockets and devices before wrapping the descriptor.
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, nil, mapErr(err)
	}
	if t := st.Mode & syscall.S_IFMT; t != syscall.S_IFREG && t != syscall.S_IFDIR {
		syscall.Close(fd)
		return nil, nil, ErrNotRegular
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		syscall.Close(fd)
		return nil, nil, mapErr(err)
	}
	f := os.NewFile(uintptr(fd), clean)
	fail := func(err error) (*os.File, fs.FileInfo, error) {
		f.Close()
		return nil, nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		return fail(mapErr(err))
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fail(ErrNotFound)
	}
	rfi, err := os.Stat(real)
	if err != nil || !os.SameFile(fi, rfi) {
		// The path changed between open and resolution: treat as a race.
		return fail(ErrNotFound)
	}
	if v := g.violation(real, fi.IsDir()); v != nil {
		return fail(v)
	}
	// Precise lexical check now that the type is known.
	if r := g.deny.Match(clean, fi.IsDir()); r.Matched {
		return fail(&DeniedError{Path: clean, Rule: r.Rule})
	}
	return f, fi, nil
}

// List returns the visible entries of a directory. Denied entries and entries
// whose symlink target is denied or outside the roots are omitted; hide rules
// omit entries too, but only for the entries themselves (browsing into a
// hidden directory shows its contents).
func (g *Guard) List(p string) ([]Entry, error) {
	f, fi, err := g.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !fi.IsDir() {
		return nil, ErrNotDir
	}
	dir := filepath.Clean(p)
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, mapErr(err)
	}

	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue // vanished between ReadDir and Info
		}
		child := filepath.Join(dir, de.Name())
		e := Entry{
			Name:      de.Name(),
			IsDir:     de.IsDir(),
			IsSymlink: de.Type()&fs.ModeSymlink != 0,
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			Mode:      info.Mode(),
		}
		if e.IsSymlink {
			real, err := filepath.EvalSymlinks(child)
			if err != nil {
				e.Broken = true
			} else {
				if rfi, err := os.Stat(real); err == nil {
					e.IsDir = rfi.IsDir()
				}
				if g.violation(real, e.IsDir) != nil {
					continue
				}
			}
		}
		// dir itself already passed Open's checks, so only the child matters.
		if g.deny.MatchBelow(dir, child, e.IsDir).Matched {
			continue
		}
		if g.hide.MatchBelow(dir, child, e.IsDir).Matched {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
