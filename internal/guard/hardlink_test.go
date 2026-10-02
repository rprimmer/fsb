package guard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A hard link is another name for the same file, so a path rule cannot see it.
// The guard therefore recognizes a regular file that has several names by its
// identity (device and inode): if any of its names is denied, every name is.

func link(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Link(from, to); err != nil {
		t.Skip("hard links not supported here:", err)
	}
}

func TestHardLinkToADeniedFileIsRefusedWhateverItIsCalled(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkRebuildMin, fx.g.linkSync = 0, true // let the test create links after the index exists
	for _, secret := range []string{".ssh/id_ed25519", "work/.env", "work/server.pem", "work/id.key", "work/.env.local"} {
		write(t, filepath.Join(fx.home, secret), "TOP-SECRET")
	}
	// An ordinary file with two names must stay readable, and it builds the index.
	write(t, filepath.Join(fx.home, "proj", "a.txt"), "ordinary")
	link(t, filepath.Join(fx.home, "proj", "a.txt"), filepath.Join(fx.home, "proj", "b.txt"))
	for _, name := range []string{"a.txt", "b.txt"} {
		f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", name))
		if err != nil {
			t.Fatalf("%s (an ordinary hard-linked file) must stay reachable: %v", name, err)
		}
		f.Close()
	}

	// Links made afterwards, under names no rule matches.
	names := map[string]string{
		".ssh/id_ed25519": "n1.txt", "work/.env": "n2.txt", "work/server.pem": "n3.txt", "work/id.key": "n4.txt", "work/.env.local": "n5.txt",
	}
	for secret, alias := range names {
		link(t, filepath.Join(fx.home, secret), filepath.Join(fx.home, "proj", alias))
	}
	missing := errorClass(fx.g, filepath.Join(fx.home, "proj", "missing.txt"))
	for secret, alias := range names {
		p := filepath.Join(fx.home, "proj", alias)
		if f, _, err := fx.g.Open(p); err == nil {
			f.Close()
			t.Errorf("%s is a second name for %s but was served", alias, secret)
		}
		if _, err := fx.g.Head(p, 20); err == nil {
			t.Errorf("Head(%s) succeeded", alias)
		}
		if got := errorClass(fx.g, p); got != missing {
			t.Errorf("%s fails as %q, unlike a missing path (%q)", alias, got, missing)
		}
	}
	// Listings agree with Open (law: listed implies openable).
	es, _ := fx.g.List(filepath.Join(fx.home, "proj"))
	for _, e := range es {
		if _, ok := map[string]bool{"n1.txt": true, "n2.txt": true, "n3.txt": true, "n4.txt": true, "n5.txt": true}[e.Name]; ok {
			t.Errorf("listing shows %s", e.Name)
		}
	}
	// The secrets' own names are refused as before, and the ordinary pair is untouched.
	for _, name := range []string{"a.txt", "b.txt"} {
		if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", name)); err != nil {
			t.Errorf("%s: %v", name, err)
		} else {
			f.Close()
		}
	}
}

// A new name that appears after the index was built is found by rebuilding it.
func TestHardLinkCreatedAfterTheIndexWasBuiltIsStillFound(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkRebuildMin, fx.g.linkSync = 0, true
	write(t, filepath.Join(fx.home, "work", ".env"), "SECRET")
	write(t, filepath.Join(fx.home, "proj", "x.txt"), "x")
	link(t, filepath.Join(fx.home, "proj", "x.txt"), filepath.Join(fx.home, "proj", "y.txt"))
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "y.txt")); err != nil { // builds the index
		t.Fatal(err)
	} else {
		f.Close()
	}
	link(t, filepath.Join(fx.home, "work", ".env"), filepath.Join(fx.home, "proj", "late.txt"))
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "late.txt")); err == nil {
		f.Close()
		t.Error("a hard link made after the index was built was served")
	}
}

// The other name may be outside the served roots. Locations named with Protect
// are searched too, which is how the credential folders of every home are covered.
func TestHardLinkToAFileOutsideTheRootsIsFoundThroughProtect(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkRebuildMin, fx.g.linkSync = 0, true
	outsideSecret := filepath.Join(fx.outside, ".ssh", "id_rsa") // its own path is denied by rule everywhere
	write(t, outsideSecret, "SECRET")
	link(t, outsideSecret, filepath.Join(fx.home, "proj", "innocent.txt"))
	// The roots alone cannot see the other name.
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "innocent.txt")); err != nil {
		t.Fatalf("precondition: the other name is outside every root and unprotected: %v", err)
	} else {
		f.Close()
	}
	fx.g.Protect(filepath.Join(fx.outside, ".ssh"))
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "innocent.txt")); err == nil {
		f.Close()
		t.Error("a name for a protected credential file was served")
	}
}

func TestLinkIndexWalkIsBounded(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkRebuildMin, fx.g.linkSync = 0, true
	fx.g.linkWalkMax = 3 // far fewer entries than the tree has
	write(t, filepath.Join(fx.home, "proj", "x.txt"), "x")
	link(t, filepath.Join(fx.home, "proj", "x.txt"), filepath.Join(fx.home, "proj", "y.txt"))
	done := make(chan struct{})
	go func() {
		f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "y.txt"))
		if err == nil {
			f.Close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the index walk did not stop at its limit")
	}
}

// The index is built in the background. Until it is ready a file with several
// names is refused (it cannot yet be shown to be harmless); a file with one name
// is served at once; and once the walk finishes ordinary linked files are served.
func TestFilesWithSeveralNamesAreRefusedUntilTheIndexIsReady(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkRebuildMin = 0
	write(t, filepath.Join(fx.home, "proj", "x.txt"), "x")
	link(t, filepath.Join(fx.home, "proj", "x.txt"), filepath.Join(fx.home, "proj", "y.txt"))
	release := make(chan struct{})
	fx.g.linkWalkHook = func() { <-release }
	fx.g.StartLinkIndex()

	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "hello.txt")); err != nil {
		t.Fatalf("a file with one name must not wait for the index: %v", err)
	} else {
		f.Close()
	}
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "y.txt")); err == nil {
		f.Close()
		t.Error("a file with several names was served before the index was ready")
	}
	es, _ := fx.g.List(filepath.Join(fx.home, "proj"))
	for _, e := range es {
		if e.Name == "x.txt" || e.Name == "y.txt" {
			t.Errorf("listing shows %s before the index is ready", e.Name)
		}
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		fx.g.links.mu.Lock()
		ready := fx.g.links.ready
		fx.g.links.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the index never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, name := range []string{"x.txt", "y.txt"} {
		f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", name))
		if err != nil {
			t.Errorf("%s must be served once the index is ready: %v", name, err)
			continue
		}
		f.Close()
	}
}

// openOK reports whether Open serves p.
func openOK(g *Guard, p string) bool {
	f, _, err := g.Open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// The tests above set linkRebuildMin to 0. These keep the production spacing
// between walks, so the time between "a link appeared" and "the next walk may
// run" is exercised too: a file the index cannot vouch for must be refused
// throughout that time, not only while a walk is running.

func TestNewHardLinkIsRefusedDuringTheRebuildCooldown(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync = true // deterministic walks; the cooldown stays at its default
	write(t, filepath.Join(fx.home, "proj", "x.txt"), "x")
	link(t, filepath.Join(fx.home, "proj", "x.txt"), filepath.Join(fx.home, "proj", "y.txt"))
	if !openOK(fx.g, filepath.Join(fx.home, "proj", "y.txt")) { // builds the index
		t.Fatal("ordinary linked file refused")
	}
	write(t, filepath.Join(fx.home, "work", ".env"), "SECRET")
	link(t, filepath.Join(fx.home, "work", ".env"), filepath.Join(fx.home, "proj", "alias.txt"))
	if openOK(fx.g, filepath.Join(fx.home, "proj", "alias.txt")) {
		t.Error("a new link to a denied file was served before the index had looked at it")
	}
	if openOK(fx.g, filepath.Join(fx.home, "proj", "alias.txt")) {
		t.Error("a new link to a denied file was served on a second try within the cooldown")
	}
	// Once a walk is allowed, it finds the denied name and the refusal stands.
	fx.g.links.mu.Lock()
	fx.g.links.lastStart = time.Time{}
	fx.g.links.mu.Unlock()
	if openOK(fx.g, filepath.Join(fx.home, "proj", "alias.txt")) {
		t.Error("a link to a denied file was served after the index was rebuilt")
	}
	if !openOK(fx.g, filepath.Join(fx.home, "proj", "x.txt")) {
		t.Error("the ordinary pair must stay reachable")
	}
}

// The index records the names a file had when it was walked. If they change
// (a rename, a removed or added name, the inode reused), what it recorded no
// longer describes the file, and the file is refused until a walk has looked again.
func TestStaleIndexEntriesAreNotTrusted(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, proj, work string)
	}{
		{"rename into a denied name", func(t *testing.T, proj, work string) {
			if err := os.Rename(filepath.Join(proj, "a.txt"), filepath.Join(proj, ".env")); err != nil {
				t.Fatal(err)
			}
		}},
		{"add a denied name", func(t *testing.T, proj, work string) {
			link(t, filepath.Join(proj, "a.txt"), filepath.Join(work, "server.pem"))
		}},
		{"replace a name with a denied one (same link count)", func(t *testing.T, proj, work string) {
			if err := os.Remove(filepath.Join(proj, "a.txt")); err != nil {
				t.Fatal(err)
			}
			link(t, filepath.Join(proj, "b.txt"), filepath.Join(work, ".env"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.g.linkSync = true
			proj, work := filepath.Join(fx.home, "proj"), filepath.Join(fx.home, "work")
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(proj, "a.txt"), "ordinary")
			link(t, filepath.Join(proj, "a.txt"), filepath.Join(proj, "b.txt"))
			if !openOK(fx.g, filepath.Join(proj, "b.txt")) { // builds the index
				t.Fatal("ordinary linked file refused")
			}
			c.change(t, proj, work)
			if openOK(fx.g, filepath.Join(proj, "b.txt")) {
				t.Error("served on the strength of names that have changed (within the cooldown)")
			}
			fx.g.links.mu.Lock()
			fx.g.links.lastStart = time.Time{}
			fx.g.links.mu.Unlock()
			if openOK(fx.g, filepath.Join(proj, "b.txt")) {
				t.Error("served after the index was rebuilt, although it now has a denied name")
			}
		})
	}
}

// Renaming the denied name away makes the file ordinary again, once a walk has seen it.
func TestRenamingTheDeniedNameAwayIsServedAfterARebuild(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync = true
	proj := filepath.Join(fx.home, "proj")
	write(t, filepath.Join(proj, ".env"), "x")
	link(t, filepath.Join(proj, ".env"), filepath.Join(proj, "b.txt"))
	if openOK(fx.g, filepath.Join(proj, "b.txt")) {
		t.Fatal("precondition: b.txt is another name for .env")
	}
	if err := os.Rename(filepath.Join(proj, ".env"), filepath.Join(proj, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if openOK(fx.g, filepath.Join(proj, "b.txt")) {
		t.Error("served before a walk had looked at the new names")
	}
	fx.g.links.mu.Lock()
	fx.g.links.lastStart = time.Time{}
	fx.g.links.mu.Unlock()
	if !openOK(fx.g, filepath.Join(proj, "b.txt")) {
		t.Error("an ordinary pair must be served once the index has seen it")
	}
}

// From the independent review of f28c274. A file that had a single name while
// a walk ran is not in the index; that must not be read as "its other names are
// where the walker cannot see", once it has a second name again.
func TestFileWithOneNameDuringTheWalkIsNotExempted(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync = true
	proj, work := filepath.Join(fx.home, "proj"), filepath.Join(fx.home, "work")
	write(t, filepath.Join(proj, "x.txt"), "x")
	link(t, filepath.Join(proj, "x.txt"), filepath.Join(proj, "y.txt"))
	if !openOK(fx.g, filepath.Join(proj, "y.txt")) {
		t.Fatal("build")
	}
	write(t, filepath.Join(work, ".env"), "SECRET")
	alias := filepath.Join(proj, "alias.txt")
	link(t, filepath.Join(work, ".env"), alias)
	if openOK(fx.g, alias) {
		t.Fatal("served at once")
	}
	if err := os.Remove(alias); err != nil { // .env has one name while the next walk runs
		t.Fatal(err)
	}
	write(t, filepath.Join(proj, "p1"), "p")
	link(t, filepath.Join(proj, "p1"), filepath.Join(proj, "p2"))
	fx.g.links.mu.Lock()
	fx.g.links.lastStart = time.Time{}
	fx.g.links.mu.Unlock()
	openOK(fx.g, filepath.Join(proj, "p1")) // an unverified file starts the walk
	link(t, filepath.Join(work, ".env"), alias)
	if openOK(fx.g, alias) {
		t.Error("another name for work/.env was served")
	}
}

// From the same review: names changed while the walk runs, so that it records
// too few of them with the right count. The hook makes the change right after
// the walk has looked at a.txt (".env" sorts before it, "zzz" after).
func TestNamesChangedDuringTheWalkAreNotTrusted(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync = true
	proj := filepath.Join(fx.home, "proj")
	write(t, filepath.Join(proj, "a.txt"), "SECRET-TO-BE")
	link(t, filepath.Join(proj, "a.txt"), filepath.Join(proj, "zzz_m.txt"))
	fx.g.linkVisitHook = func(p string) {
		if filepath.Base(p) == "a.txt" {
			link(t, filepath.Join(proj, "a.txt"), filepath.Join(proj, ".env"))
			os.Remove(filepath.Join(proj, "zzz_m.txt"))
		}
	}
	fx.g.StartLinkIndex()
	fx.g.linkVisitHook = nil
	if openOK(fx.g, filepath.Join(proj, "a.txt")) {
		t.Error("a.txt was served although it is another name for proj/.env")
	}
}

// Deciding a file with many names must not cost work for every name each time
// (a backup tree made with cp -al has one name per snapshot).
func TestManyNamesOfOneFileListQuickly(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync = true
	d := filepath.Join(fx.home, "proj", "many")
	write(t, filepath.Join(d, "0"), "x")
	for i := 1; i < 500; i++ {
		link(t, filepath.Join(d, "0"), filepath.Join(d, itoa(i)))
	}
	openOK(fx.g, filepath.Join(d, "0")) // builds the index
	t0 := time.Now()
	es, err := fx.g.List(d)
	if err != nil || len(es) != 500 {
		t.Fatalf("%d entries, %v", len(es), err)
	}
	if took := time.Since(t0); took > 2*time.Second {
		t.Errorf("listing 500 names of one file took %v", took)
	}
}

// On a file system with whole-second timestamps a link made just after a walk
// began can carry a ctime earlier than the start. So a ctime counts as "before
// the walk" only with a margin, and a link made just before is refused until a
// later walk.
func TestCtimeMarginRefusesLinksMadeJustBeforeAWalk(t *testing.T) {
	fx := newFixture(t)
	fx.g.linkSync, fx.g.linkCtimeMargin = true, defaultCtimeMargin
	proj := filepath.Join(fx.home, "proj")
	write(t, filepath.Join(proj, "x.txt"), "x")
	link(t, filepath.Join(proj, "x.txt"), filepath.Join(proj, "y.txt"))
	if openOK(fx.g, filepath.Join(proj, "y.txt")) {
		t.Error("a pair linked within the margin before the walk was trusted")
	}
	fx.g.linkCtimeMargin = 0 // as if the margin had passed
	fx.g.links.mu.Lock()
	fx.g.links.lastStart = time.Time{}
	fx.g.links.mu.Unlock()
	if !openOK(fx.g, filepath.Join(proj, "y.txt")) {
		t.Error("the pair must be served once a walk began after it changed")
	}
}
