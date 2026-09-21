package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// Property tests for the laws in algebra/ (section "Access decision").

// lawGuard builds a tree that exercises the awkward cases together: a dir-only
// deny rule meeting a plain file of the same name, symlinks in every
// direction, hidden and denied names, and names that differ only by folding.
func lawGuard(t *testing.T) (*Guard, string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	for _, f := range []string{
		"a/plain.txt", "a/cache", "b/cache/inner.txt", "b/other.txt", "c/deep/er/file.txt",
		"c/secret.key", "c/Notes.md", ".hid/x.txt", "node_modules/p/i.js", "d/.env", "d/ok.txt",
		"e/Straße.txt", "e/ﬁle.txt",
	} {
		write(t, filepath.Join(home, f), "x")
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(filepath.Join(home, "b", "cache"), filepath.Join(home, "a", "link-to-denied-dir")))
	must(os.Symlink(filepath.Join(home, "a", "plain.txt"), filepath.Join(home, "a", "link-ok")))
	must(os.Symlink(filepath.Join(home, "c", "secret.key"), filepath.Join(home, "a", "link-to-key")))
	must(os.Symlink(filepath.Join(home, "a", "plain.txt"), filepath.Join(home, "a", "renamed.key"))) // lexically denied
	must(os.Symlink(filepath.Join(home, "c"), filepath.Join(home, "a", "link-dir")))
	must(os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(home, "a", "broken")))
	deny, err := rules.Parse(strings.NewReader("cache/\n*.key\n.env\n"), rules.ParseOptions{Home: home})
	must(err)
	hide, err := rules.Parse(strings.NewReader(".hid\nnode_modules\n"), rules.ParseOptions{Home: home, AllowNegation: true})
	must(err)
	g, err := New([]string{home}, deny, hide)
	must(err)
	return g, home
}

func allDirs(t *testing.T, home string) []string {
	var dirs []string
	filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs
}

// Law "listed implies openable": everything a listing shows can be opened by
// its path. (The reverse is not required: hidden entries are not listed but can
// be opened.)
func TestLawListedImpliesOpenable(t *testing.T) {
	g, home := lawGuard(t)
	for _, dir := range allDirs(t, home) {
		es, err := g.List(dir)
		if err != nil {
			continue // a denied or hidden-by-deny directory is not listable; fine
		}
		for _, e := range es {
			if e.Broken {
				continue // a dangling symlink is listed as such and has nothing to open
			}
			p := filepath.Join(dir, e.Name)
			f, _, err := g.Open(p)
			if err != nil {
				t.Errorf("%s is listed but Open refuses it: %v", p, err)
				continue
			}
			f.Close()
		}
	}
}

// Law "found implies listed": every search result appears in the listing of its
// own folder, so search can never show what browsing would not.
func TestLawSearchResultsAreListed(t *testing.T) {
	g, home := lawGuard(t)
	for _, q := range []string{"a", "e", "txt", "cache", "key", "s", "file", "strasse", "fi"} {
		_, _, err := g.Search(context.Background(), home, q, SearchOptions{MaxResults: 1000, MaxVisited: 10000}, func(m SearchMatch) error {
			es, err := g.List(filepath.Dir(m.Path))
			if err != nil {
				t.Errorf("search reported %s but its folder cannot be listed: %v", m.Path, err)
				return nil
			}
			for _, e := range es {
				if e.Name == m.Name {
					return nil
				}
			}
			t.Errorf("search reported %s (query %q) but the listing of its folder does not contain it", m.Path, q)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Law "denied is absent": a name the deny rules match is never listed, searched
// or opened, and Open answers it exactly as it answers a missing path.
func TestLawDeniedIsAbsentEverywhere(t *testing.T) {
	g, home := lawGuard(t)
	denied := []string{"c/secret.key", "d/.env", "b/cache", "b/cache/inner.txt", "a/renamed.key", "a/link-to-key", "a/link-to-denied-dir"}
	missing := errorClass(g, filepath.Join(home, "no/such/path"))
	for _, d := range denied {
		p := filepath.Join(home, d)
		if got := errorClass(g, p); got != missing {
			t.Errorf("Open(%s) fails as %q, a missing path as %q", d, got, missing)
		}
		es, err := g.List(filepath.Dir(p))
		if err == nil {
			for _, e := range es {
				if e.Name == filepath.Base(p) {
					t.Errorf("%s is denied but listed", d)
				}
			}
		}
	}
	_, _, _ = g.Search(context.Background(), home, "", SearchOptions{MaxResults: 10, MaxVisited: 10}, nil)
	for _, q := range []string{"key", "env", "cache", "inner", "link"} {
		g.Search(context.Background(), home, q, SearchOptions{MaxResults: 1000, MaxVisited: 10000}, func(m SearchMatch) error {
			for _, d := range denied {
				if m.Path == filepath.Join(home, d) {
					t.Errorf("search %q reported the denied %s", q, d)
				}
			}
			return nil
		})
	}
}

func errorClass(g *Guard, p string) string {
	f, _, err := g.Open(p)
	if err == nil {
		f.Close()
		return "ok"
	}
	if errors.Is(err, ErrNotFound) {
		return "not found"
	}
	return err.Error()
}

// Law "canonical form is stable": CanonPath is idempotent.
func TestLawCanonPathIsIdempotent(t *testing.T) {
	for _, p := range []string{"/", "/Users/me", "/System/Volumes/Data/Users/me", "/private/var/x", "/System/Volumes/Data", "/System/Volumes/Data/System/Volumes/Data/x", "/a/../b"} {
		c := CanonPath(p)
		if CanonPath(c) != c {
			t.Errorf("CanonPath(CanonPath(%q)) = %q, want %q", p, CanonPath(c), c)
		}
	}
}
