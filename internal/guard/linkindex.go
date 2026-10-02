package guard

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Path rules cannot see a hard link: it is another name for the same file. So a
// regular file that has more than one name is judged by identity (device and
// inode): if any of its names is denied, all of them are.
//
// The names come from an index of every regular file with more than one name
// under the served roots and under the locations named with Protect (the
// credential folders, which may lie outside the roots). Files with a single
// name, nearly all of them, never touch the index.
//
// Building the index means visiting every file, which takes seconds on a large
// home folder, so it is built in the background (StartLinkIndex, called at
// startup) and never on the path of a request.
//
// A file with several names is served only when the index accounts for every
// one of them, now: the walk found as many distinct names as the file has
// links, none is denied, and on each use every recorded name is still a real
// path (no symbolic link along it) to this very file. Nothing about the file
// itself is trusted to reveal a change: renaming or moving a folder renames
// the files in it without touching them. Anything else (the index is not
// ready, a name lies where the walk does not look or could not read, or the
// names changed) is refused until a walk has looked again; guessing "allowed"
// would defeat the point.

type fileID struct{ dev, ino uint64 }

// nameKey identifies one name of a file: the folder holding it and the name in
// that folder. Two paths can spell one name (a firmlink, a folder walked
// twice); two names never share a key.
type nameKey struct {
	dir  fileID
	base string
}

// linkIndex maps a file's identity to what the last walk saw of it.
type linkIndex struct {
	mu        sync.Mutex
	byID      map[fileID]linkEntry
	ready     bool          // a walk has completed
	building  bool          // a walk is running
	lastStart time.Time     // when the last walk (running or not) started
	lastTook  time.Duration // how long the last completed walk took
}

// linkEntry is what a walk saw of a file with several names.
type linkEntry struct {
	names  []string // real paths, one per distinct name
	denied bool     // a deny rule matches one of them
}

// linkMemo remembers decisions within one listing or search, so a folder
// holding many names of one file costs one check, not one per name.
type linkMemo map[fileID]bool

// Limits of an index walk. It runs in the background, so they only need to keep
// a pathological tree from running forever. Files whose names lie beyond the
// limit are refused.
const (
	defaultLinkWalkMax   = 20_000_000
	defaultLinkWalkTime  = 10 * time.Minute
	defaultLinkRebuildIn = 15 * time.Second // minimum time between walks
)

// Protect names locations (folders or files, such as ~/.ssh) whose files must
// not be reachable under other names, even when the location is not under a
// served root: they are searched for other names of a file, along with the roots.
// Call it before serving.
func (g *Guard) Protect(paths ...string) {
	g.links.mu.Lock()
	defer g.links.mu.Unlock()
	for _, p := range paths {
		// Recorded names must be real paths, so resolve symbolic links
		// (such as /var on macOS) where the location exists.
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		g.protect = append(g.protect, canonPath(p))
	}
	g.links.ready = false // the next walk includes the new locations
}

// StartLinkIndex begins building the hard-link index in the background.
func (g *Guard) StartLinkIndex() {
	g.links.mu.Lock()
	defer g.links.mu.Unlock()
	g.startWalkLocked()
}

func idOf(fi fs.FileInfo) (id fileID, nlink uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return fileID{}, 0, false
	}
	return fileID{uint64(st.Dev), st.Ino}, uint64(st.Nlink), true
}

// hardLinkToDenied reports whether fi, a file about to be served or listed, has
// another name that a deny rule matches, or cannot be shown not to.
func (g *Guard) hardLinkToDenied(fi fs.FileInfo) bool { return g.hardLinkToDeniedMemo(fi, nil) }

// hardLinkToDeniedMemo is hardLinkToDenied, remembering decisions in memo
// (which may be nil) for the rest of one listing or search.
func (g *Guard) hardLinkToDeniedMemo(fi fs.FileInfo, memo linkMemo) bool {
	id, nlink, ok := idOf(fi)
	if !ok || !fi.Mode().IsRegular() || nlink < 2 {
		return false
	}
	if v, ok := memo[id]; ok {
		return v
	}
	v := g.judgeLinked(id, nlink)
	if memo != nil {
		memo[id] = v
	}
	return v
}

func (g *Guard) judgeLinked(id fileID, nlink uint64) bool {
	idx := &g.links
	for walked := false; ; walked = true {
		idx.mu.Lock()
		if !idx.ready {
			g.startWalkLocked()
		}
		ready := idx.ready
		e, known := idx.byID[id]
		idx.mu.Unlock()
		if !ready {
			return true // still walking (never the case when linkSync is set)
		}
		if known && uint64(len(e.names)) == nlink && namesStillName(id, e.names) {
			return e.denied
		}
		// Not accounted for: refuse, unless a walk may run now (with linkSync,
		// tests, it completes here and the file is judged again). Walks are
		// spaced by at least twice the time the last one took, so a huge tree
		// cannot keep the machine busy walking.
		idx.mu.Lock()
		canWalk := !walked && time.Since(idx.lastStart) >= max(g.linkRebuildMin, 2*idx.lastTook)
		if canWalk {
			g.startWalkLocked()
		}
		idx.mu.Unlock()
		if !canWalk {
			return true
		}
	}
}

// namesStillName reports whether every recorded name is, now, a distinct name
// of the file id: a real path (no symbolic link along it, so where it appears
// to be is where it is) whose last component is this file.
func namesStillName(id fileID, names []string) bool {
	keys := make(map[nameKey]bool, len(names))
	for _, n := range names {
		fi, err := os.Lstat(n)
		if err != nil || !fi.Mode().IsRegular() {
			return false
		}
		if got, _, ok := idOf(fi); !ok || got != id {
			return false
		}
		if real, err := filepath.EvalSymlinks(n); err != nil || real != n {
			return false
		}
		k, ok := nameKeyOf(n)
		if !ok || keys[k] {
			return false
		}
		keys[k] = true
	}
	return true
}

func nameKeyOf(p string) (nameKey, bool) {
	di, err := os.Lstat(filepath.Dir(p))
	if err != nil || !di.IsDir() {
		return nameKey{}, false
	}
	dir, _, ok := idOf(di)
	return nameKey{dir, filepath.Base(p)}, ok
}

func (g *Guard) anyDenied(names []string) bool {
	for _, n := range names {
		if g.deny.Match(canonPath(n), false).Matched {
			return true
		}
	}
	return false
}

// startWalkLocked starts a walk unless one is running. Caller holds the lock.
// With linkSync (tests) it runs to completion before returning.
func (g *Guard) startWalkLocked() {
	idx := &g.links
	if idx.building {
		return
	}
	idx.building = true
	idx.lastStart = time.Now()
	started := idx.lastStart
	finish := func(byID map[fileID]linkEntry) {
		for id, e := range byID {
			e.denied = g.anyDenied(e.names)
			byID[id] = e
		}
		idx.byID, idx.ready, idx.building = byID, true, false
		idx.lastTook = time.Since(started)
	}
	locations := append(append([]string(nil), g.roots...), g.protect...)
	if g.linkSync {
		finish(g.walkLinks(locations))
		return
	}
	go func() {
		byID := g.walkLinks(locations)
		idx.mu.Lock()
		finish(byID)
		idx.mu.Unlock()
	}()
}

// walkLinks visits every file under the roots and protected locations and
// returns the distinct names of those that have more than one. Folders it
// cannot read, or does not reach within its limits, leave names unfound, and
// a file with unfound names is refused.
func (g *Guard) walkLinks(locations []string) map[fileID]linkEntry {
	if g.linkWalkHook != nil {
		g.linkWalkHook()
	}
	byID := map[fileID]linkEntry{}
	dirs := map[string]fileID{} // folders visited, by path
	seenNames := map[nameKey]bool{}
	seen := 0
	deadline := time.Now().Add(g.linkWalkTime)
	for _, root := range locations {
		stop := false
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: its names stay unfound
			}
			if seen++; seen > g.linkWalkMax || (seen%1024 == 0 && time.Now().After(deadline)) {
				stop = true
				return filepath.SkipAll
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				// A dataless (cloud-only) folder is never enumerated: that could
				// make its provider download it. Its contents are not on this
				// disk, so they hold no other name of a file that is.
				if isDataless(info) {
					return filepath.SkipDir
				}
				if id, _, ok := idOf(info); ok {
					dirs[p] = id
				}
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil // symlinks, devices: only regular files have hard links that matter
			}
			id, nlink, ok := idOf(info)
			if !ok || nlink < 2 {
				return nil
			}
			dir, known := dirs[filepath.Dir(p)]
			if !known {
				k, ok := nameKeyOf(p) // a location that is itself a file
				if !ok {
					return nil
				}
				dir = k.dir
			}
			key := nameKey{dir, d.Name()}
			if seenNames[key] {
				return nil // the same name reached another way
			}
			seenNames[key] = true
			e := byID[id]
			e.names = append(e.names, p)
			byID[id] = e
			if g.linkVisitHook != nil {
				g.linkVisitHook(p)
			}
			return nil
		})
		if stop {
			break
		}
	}
	return byID
}
