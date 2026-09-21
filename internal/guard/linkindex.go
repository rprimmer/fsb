package guard

import (
	"io/fs"
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
// startup) and never on the path of a request. While it is not ready, and for a
// file with several names that it has not seen yet (a link made since), the
// file is refused: guessing "allowed" would defeat the point.

type fileID struct{ dev, ino uint64 }

// linkIndex maps a file's identity to all its known names.
type linkIndex struct {
	mu        sync.Mutex
	byID      map[fileID][]string
	ready     bool          // a walk has completed
	building  bool          // a walk is running
	complete  bool          // the last walk was not cut short by a limit
	startedAt time.Time     // when the last completed walk started
	lastStart time.Time     // when the last walk (running or not) started
	lastTook  time.Duration // how long the last completed walk took
	pending   map[fileID]time.Time
}

// Limits of an index walk. It runs in the background, so they only need to keep
// a pathological tree from running forever.
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
	if !isStat || !fi.Mode().IsRegular() {
		return fileID{}, 0, false
	}
	return fileID{uint64(st.Dev), st.Ino}, uint64(st.Nlink), true
}

// hardLinkToDenied reports whether fi, a file about to be served or listed, has
// another name that a deny rule matches, or cannot yet be shown not to.
func (g *Guard) hardLinkToDenied(fi fs.FileInfo) bool {
	id, nlink, ok := idOf(fi)
	if !ok || nlink < 2 {
		return false
	}
	idx := &g.links
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if !idx.ready {
		g.startWalkLocked()
		if !idx.ready { // still walking (never the case when linkSync is set)
			return true
		}
	}
	names, known := idx.byID[id]
	if !known && idx.complete {
		// A file with several names that the walk did not see. If a walk that
		// began after we first noticed it also missed it, the walker cannot see
		// it (it is not refused forever); otherwise it is new, and is refused
		// until a walk has looked at it.
		if first, seen := idx.pending[id]; seen && idx.startedAt.After(first) {
			return false
		}
		if _, seen := idx.pending[id]; !seen {
			if idx.pending == nil {
				idx.pending = map[fileID]time.Time{}
			}
			idx.pending[id] = time.Now()
		}
		// Walks are spaced by at least twice the time the last one took, so a
		// huge tree cannot keep the machine busy walking.
		if time.Since(idx.lastStart) >= max(g.linkRebuildMin, 2*idx.lastTook) {
			g.startWalkLocked()
		}
		names, known = idx.byID[id]
		if !known {
			return idx.building
		}
	}
	for _, n := range names {
		if g.deny.Match(n, false).Matched {
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
	finish := func(byID map[fileID][]string, complete bool) {
		idx.byID, idx.complete, idx.ready, idx.startedAt, idx.building = byID, complete, true, started, false
		idx.lastTook = time.Since(started)
	}
	if g.linkSync {
		byID, complete := g.walkLinks()
		finish(byID, complete)
		return
	}
	go func() {
		byID, complete := g.walkLinks()
		idx.mu.Lock()
		finish(byID, complete)
		idx.mu.Unlock()
	}()
}

// walkLinks visits every file under the roots and protected locations and
// returns the names of those that have more than one.
func (g *Guard) walkLinks() (map[fileID][]string, bool) {
	if g.linkWalkHook != nil {
		g.linkWalkHook()
	}
	byID := map[fileID][]string{}
	seen := 0
	deadline := time.Now().Add(g.linkWalkTime)
	complete := true
	roots := append(append([]string(nil), g.roots...), g.protect...)
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skip it
			}
			if seen++; seen > g.linkWalkMax || (seen%1024 == 0 && time.Now().After(deadline)) {
				complete = false
				return filepath.SkipAll
			}
			if !d.Type().IsRegular() {
				return nil // directories, symlinks, devices: only regular files have hard links that matter
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if id, nlink, ok := idOf(info); ok && nlink > 1 {
				byID[id] = append(byID[id], canonPath(p))
			}
			return nil
		})
	}
	return byID, complete
}
