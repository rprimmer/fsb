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
// startup) and never on the path of a request.
//
// What the index says about a file is trusted only if the file's names cannot
// have changed since before the walk began: its change time (ctime), which
// every link, unlink and rename updates and which no ordinary process can set,
// must be earlier than the walk's start and the same now. Then the walk saw all
// its names that lie where it looks. Anything else (the index is not ready yet,
// the file is new, or it changed before or during the walk or since) is refused
// until a walk has looked again: guessing "allowed" would defeat the point.

type fileID struct{ dev, ino uint64 }

// linkIndex maps a file's identity to what the last walk saw of it.
type linkIndex struct {
	mu        sync.Mutex
	byID      map[fileID]linkEntry
	ready     bool          // a walk has completed
	building  bool          // a walk is running
	complete  bool          // the last walk was not cut short by a limit
	startedAt time.Time     // when the last completed walk started
	lastStart time.Time     // when the last walk (running or not) started
	lastTook  time.Duration // how long the last completed walk took
}

// linkEntry is what a walk saw of a file with several names.
type linkEntry struct {
	names   []string
	nlink   uint64
	ctime   int64 // nanoseconds
	trusted bool  // its names were stable throughout the walk
	denied  bool  // a deny rule matches one of its names
}

// defaultCtimeMargin allows for file systems that store whole (or even pairs
// of) seconds: a ctime must be this much earlier than a walk's start to count
// as before it.
const defaultCtimeMargin = 2 * time.Second

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

// stamp is the identity, link count and change time of a regular file.
type stamp struct {
	id    fileID
	nlink uint64
	ctime int64
}

func stampOf(fi fs.FileInfo) (stamp, bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat || !fi.Mode().IsRegular() {
		return stamp{}, false
	}
	return stamp{fileID{uint64(st.Dev), st.Ino}, uint64(st.Nlink), ctimeOf(st)}, true
}

// hardLinkToDenied reports whether fi, a file about to be served or listed, has
// another name that a deny rule matches, or cannot yet be shown not to.
func (g *Guard) hardLinkToDenied(fi fs.FileInfo) bool {
	st, ok := stampOf(fi)
	if !ok || st.nlink < 2 {
		return false
	}
	idx := &g.links
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !idx.ready {
		g.startWalkLocked()
	}
	for walked := false; ; walked = true {
		if idx.ready {
			if denied, decided := idx.decide(st, g.linkCtimeMargin); decided {
				return denied
			}
		}
		// Undecided: refuse, unless a walk may run now (with linkSync, tests,
		// it completes here and the file is decided again). Walks are spaced
		// by at least twice the time the last one took, so a huge tree cannot
		// keep the machine busy walking.
		if walked || time.Since(idx.lastStart) < max(g.linkRebuildMin, 2*idx.lastTook) {
			return true
		}
		g.startWalkLocked()
	}
}

// decide judges a file by the index, if the index can vouch for it. Caller
// holds the lock.
func (idx *linkIndex) decide(st stamp, margin time.Duration) (denied, decided bool) {
	e, known := idx.byID[st.id]
	if known {
		if e.trusted && e.nlink == st.nlink && e.ctime == st.ctime {
			return e.denied, true
		}
		return false, false // changed during or since the walk
	}
	if !idx.complete {
		return false, true // a walk cut short by its limits: documented as not covered
	}
	// Unknown to a complete walk. If the file has not changed since before
	// that walk began, it had these several names throughout, so none of them
	// lies where the walk looks: none is under a root or a protected location.
	if st.ctime < idx.startedAt.Add(-margin).UnixNano() {
		return false, true
	}
	return false, false
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
	finish := func(byID map[fileID]linkEntry, complete bool) {
		before := started.Add(-g.linkCtimeMargin).UnixNano()
		for id, e := range byID {
			e.trusted = e.nlink != 0 && e.ctime < before
			e.denied = g.anyDenied(e.names)
			byID[id] = e
		}
		idx.byID, idx.complete, idx.ready, idx.startedAt, idx.building = byID, complete, true, started, false
		idx.lastTook = time.Since(started)
	}
	locations := append(append([]string(nil), g.roots...), g.protect...)
	if g.linkSync {
		byID, complete := g.walkLinks(locations)
		finish(byID, complete)
		return
	}
	go func() {
		byID, complete := g.walkLinks(locations)
		idx.mu.Lock()
		finish(byID, complete)
		idx.mu.Unlock()
	}()
}

// walkLinks visits every file under the roots and protected locations and
// returns the names of those that have more than one.
func (g *Guard) walkLinks(locations []string) (map[fileID]linkEntry, bool) {
	if g.linkWalkHook != nil {
		g.linkWalkHook()
	}
	byID := map[fileID]linkEntry{}
	seen := 0
	deadline := time.Now().Add(g.linkWalkTime)
	complete := true
	for _, root := range locations {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skip it
			}
			if seen++; seen > g.linkWalkMax || (seen%1024 == 0 && time.Now().After(deadline)) {
				complete = false
				return filepath.SkipAll
			}
			if d.IsDir() {
				// A dataless (cloud-only) folder is never enumerated: that could
				// make its provider download it. Its contents are not on this
				// disk, so they hold no other name of a file that is, and the
				// walk still counts as complete.
				if info, err := d.Info(); err != nil || isDataless(info) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // symlinks, devices: only regular files have hard links that matter
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if st, ok := stampOf(info); ok && st.nlink > 1 {
				e, seen := byID[st.id]
				if !seen {
					e.nlink, e.ctime = st.nlink, st.ctime
				} else if e.nlink != st.nlink || e.ctime != st.ctime {
					e.nlink = 0 // it changed while walking: not to be trusted
				}
				e.names = append(e.names, canonPath(p))
				byID[st.id] = e
				if g.linkVisitHook != nil {
					g.linkVisitHook(p)
				}
			}
			return nil
		})
	}
	return byID, complete
}

func (g *Guard) anyDenied(names []string) bool {
	for _, n := range names {
		if g.deny.Match(n, false).Matched {
			return true
		}
	}
	return false
}
