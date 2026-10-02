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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

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
	roots    []string      // real paths
	rootInfo []fs.FileInfo // the same roots, for comparing by identity
	protect  []string      // extra locations searched for other names of a file (see Protect)

	// Hard-link detection (see linkindex.go). The limits are fields so tests can change them.
	links           linkIndex
	linkRebuildMin  time.Duration
	linkWalkMax     int
	linkWalkTime    time.Duration
	linkCtimeMargin time.Duration // see defaultCtimeMargin
	linkSync        bool          // walk on the caller's goroutine (tests)
	linkWalkHook    func()        // called when a walk starts (tests)
	linkVisitHook   func(string)  // called after a walk records a file (tests)
	deny            *rules.Set
	hide            *rules.Set
}

// New creates a Guard. Each root must exist and is resolved to its real path.
func New(roots []string, deny, hide *rules.Set) (*Guard, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one root is required")
	}
	g := &Guard{deny: deny, hide: hide, linkRebuildMin: defaultLinkRebuildIn, linkWalkMax: defaultLinkWalkMax, linkWalkTime: defaultLinkWalkTime, linkCtimeMargin: defaultCtimeMargin}
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
		// The same folder given twice, under any spelling, is served once.
		if slices.ContainsFunc(g.rootInfo, func(o os.FileInfo) bool { return os.SameFile(o, fi) }) {
			continue
		}
		g.roots = append(g.roots, canonPath(real))
		g.rootInfo = append(g.rootInfo, fi)
	}
	return g, nil
}

// ServesSystemRoot reports whether one of the roots is the system root, /,
// judged by identity rather than by how it was spelled.
func (g *Guard) ServesSystemRoot() bool {
	sys, err := os.Stat("/")
	if err != nil {
		return true // cannot tell: assume the worst
	}
	return slices.ContainsFunc(g.rootInfo, func(fi fs.FileInfo) bool { return os.SameFile(fi, sys) })
}

// Roots returns the resolved root paths.
func (g *Guard) Roots() []string { return append([]string(nil), g.roots...) }

// fold normalizes a path for case- and normalization-insensitive comparison.
func fold(s string) string { return rules.Fold(s) }

// inRoots reports whether real is a root or lies beneath one. It is an allow
// decision, so it is made by identity and not by comparing spellings: the
// leading components of real that correspond to the root must be the very
// directory the root is. Comparing folded text would be right on a
// case-insensitive volume but would admit a different sibling folder on a
// case-sensitive one ("Public" for a root of "public").
func (g *Guard) inRoots(real string) bool {
	comps := strings.Split(strings.Trim(real, "/"), "/")
	if real == "/" {
		comps = nil
	}
	for i, r := range g.roots {
		if r == "/" {
			return true
		}
		k := len(strings.Split(strings.Trim(r, "/"), "/"))
		if len(comps) < k {
			continue
		}
		fi, err := os.Stat("/" + strings.Join(comps[:k], "/"))
		if err == nil && os.SameFile(fi, g.rootInfo[i]) {
			return true
		}
	}
	return false
}

// violation checks a resolved real path against the roots and deny rules.
func (g *Guard) violation(real string, isDir bool) *DeniedError {
	real = canonPath(real)
	if !g.inRoots(real) {
		return &DeniedError{Path: real, Rule: "outside configured roots"}
	}
	if r := g.deny.Match(real, isDir); r.Matched {
		return &DeniedError{Path: real, Rule: r.Rule}
	}
	return nil
}

// CanonPath reduces a path to its ordinary form on platforms with path aliases
// (on macOS, /System/Volumes/Data/Users/me becomes /Users/me). Callers that
// build deny rules from a directory, such as the home directory, use it so
// that the rules and the request paths are compared in the same form.
func CanonPath(p string) string { return canonPath(p) }

// RealPath resolves symlinks and path aliases in p, for a directory that rules
// are built from (the home directory): the guard compares rules with real
// paths, so a rule for ~/.ssh must be expressed with the real home directory
// even when $HOME is reached through a symlink. If p cannot be resolved it is
// returned in canonical form.
func RealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return canonPath(p)
}

// canonical validates and cleans a client-supplied path. URL decoding is the
// HTTP layer's job and happens exactly once; a literal "%2e%2e" here is just a
// file name. Aliases of the same location are reduced to one form (see
// canonPath) so a deny rule cannot be dodged by naming the path differently.
func canonical(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return "", ErrNotFound
	}
	return canonPath(filepath.Clean(p)), nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR),
		errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENAMETOOLONG):
		return ErrNotFound
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return ErrPermission
	case errors.Is(err, syscall.ENXIO), errors.Is(err, syscall.EOPNOTSUPP):
		// Opening a UNIX domain socket's path fails at the syscall itself,
		// before there is a descriptor to fstat: EOPNOTSUPP on macOS,
		// historically ENXIO on Linux. Sockets are exactly the kind of
		// special file Open already refuses after opening a FIFO or device
		// (see the fstat check below); this is the same refusal, just
		// signaled earlier, and must map the same way rather than falling
		// through as an unrecognized error.
		return ErrNotRegular
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
	f, fi, _, err := g.open(p)
	return f, fi, err
}

// open is Open plus the canonical real path of what was opened. Callers that go
// on to examine the children of a directory (listing, search) must judge them
// by that real path and not by however the directory was reached: a symlink to
// a directory does not change what is inside it.
func (g *Guard) open(p string) (*os.File, fs.FileInfo, string, error) {
	clean, err := canonical(p)
	if err != nil {
		return nil, nil, "", err
	}
	// Lexical pre-check, before touching the filesystem. The type of the final
	// component is not known yet, so it is treated as a file here (every
	// ancestor is a directory regardless); a rule that applies only to
	// directories is decided below, once the type is known. Assuming a
	// directory instead would refuse a plain file that shares its name with a
	// "dir/" rule although listings show it.
	if r := g.deny.Match(clean, false); r.Matched {
		return nil, nil, "", &DeniedError{Path: clean, Rule: r.Rule}
	}

	fd, err := syscall.Open(clean, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		merr := mapErr(err)
		if errors.Is(merr, ErrPermission) {
			// An unreadable file inside a denied place must look exactly like a
			// missing one, however the path reached it, or its existence leaks.
			if v := g.deniedByAncestor(clean); v != nil {
				return nil, nil, "", v
			}
		}
		return nil, nil, "", merr
	}
	// Reject FIFOs, sockets and devices before wrapping the descriptor.
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, nil, "", mapErr(err)
	}
	if t := st.Mode & syscall.S_IFMT; t != syscall.S_IFREG && t != syscall.S_IFDIR {
		syscall.Close(fd)
		return nil, nil, "", ErrNotRegular
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		syscall.Close(fd)
		return nil, nil, "", mapErr(err)
	}
	f := os.NewFile(uintptr(fd), clean)
	fail := func(err error) (*os.File, fs.FileInfo, string, error) {
		f.Close()
		return nil, nil, "", err
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
	// Another name for a credential file (a hard link) that no path rule can see.
	if g.hardLinkToDenied(fi) {
		return fail(&DeniedError{Path: clean, Rule: "hard link to a denied file"})
	}
	return f, fi, canonPath(real), nil
}

// deniedByAncestor decides, for a path that could not be opened because of a
// permission error, whether it lies inside a denied place. It resolves the
// longest prefix that can be resolved: an unsearchable directory stops the
// resolution of what is inside it, but not of the directory itself.
func (g *Guard) deniedByAncestor(clean string) *DeniedError {
	p, rest := clean, ""
	for p != "/" && p != "." && p != "" {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return g.violation(filepath.Join(real, rest), true)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = filepath.Dir(p)
	}
	return nil
}

// List returns the visible entries of a directory. Denied entries and entries
// whose symlink target is denied or outside the roots are omitted; hide rules
// omit entries too, but only for the entries themselves (browsing into a
// hidden directory shows its contents).
func (g *Guard) List(p string) ([]Entry, error) {
	var out []Entry
	err := g.ListFunc(p, 1024, func(es []Entry) error {
		out = append(out, es...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if out == nil {
		out = []Entry{}
	}
	return out, nil
}

// ListFunc streams the visible entries of a directory to fn in batches of up
// to batch directory entries (filtering can make a batch smaller; empty
// batches are skipped). Entries arrive in directory order, not sorted. An
// error returned before fn is first called means nothing was listed; an error
// afterwards means the listing is incomplete.
func (g *Guard) ListFunc(p string, batch int, fn func([]Entry) error) error {
	if batch < 1 {
		batch = 1
	}
	f, fi, dir, err := g.open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if !fi.IsDir() {
		return ErrNotDir
	}
	if err := refuseDataless(fi); err != nil {
		return err
	}
	// dir is the real location: children are judged by where they really are,
	// not by the symlink (or alias) the directory was reached through.
	for {
		des, err := f.ReadDir(batch)
		out := make([]Entry, 0, len(des))
		for _, de := range des {
			if e, ok := g.entry(dir, de); ok {
				out = append(out, e)
			}
		}
		if len(out) > 0 {
			if ferr := fn(out); ferr != nil {
				return ferr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return mapErr(err)
		}
	}
}

// entry builds the listing entry for de, or reports false if it must not be
// shown (vanished, denied, hidden, or a symlink to a denied/outside target).
func (g *Guard) entry(dir string, de fs.DirEntry) (Entry, bool) {
	info, err := de.Info()
	if err != nil {
		return Entry{}, false // vanished between ReadDir and Info
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
				// Show the target's size and date, not the link text's.
				e.IsDir = rfi.IsDir()
				e.Size = rfi.Size()
				e.ModTime = rfi.ModTime()
			}
			if g.violation(real, e.IsDir) != nil {
				return Entry{}, false
			}
		}
	}
	// Another name for a denied file, as Open would refuse it.
	subject := info
	if e.IsSymlink && !e.Broken {
		if rfi, err := os.Stat(child); err == nil {
			subject = rfi
		}
	}
	if g.hardLinkToDenied(subject) {
		return Entry{}, false
	}
	// dir itself already passed Open's checks, so only the child matters.
	if g.deny.MatchBelow(dir, child, e.IsDir).Matched {
		return Entry{}, false
	}
	if g.hide.MatchBelow(dir, child, e.IsDir).Matched {
		return Entry{}, false
	}
	return e, true
}
