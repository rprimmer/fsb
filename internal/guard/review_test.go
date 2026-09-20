package guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// Regression tests for findings from an independent security review.

func userDeny(t *testing.T, home, text string) *rules.Set {
	t.Helper()
	s, err := rules.Parse(strings.NewReader(text), rules.ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Finding 1: rules were applied to a listing's children using the path the
// directory was reached by, so listing through a symlink to a directory that
// contains a denied entry showed that entry's name and metadata.
func TestListingThroughASymlinkedDirectoryHonoursDenyRules(t *testing.T) {
	fx := newFixture(t)
	if err := os.Symlink(fx.home, filepath.Join(fx.home, "homelink")); err != nil {
		t.Fatal(err)
	}
	es, err := fx.g.List(filepath.Join(fx.home, "homelink"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(names(es), ".ssh") {
		t.Errorf("listing through a symlink to the home directory exposes .ssh: %v", names(es))
	}

	// The same for a user-written rule.
	put(t, filepath.Join(fx.home, "docs", "private", "x.txt"), []byte("hidden"))
	put(t, filepath.Join(fx.home, "docs", "public.txt"), []byte("ok"))
	if err := os.Symlink(filepath.Join(fx.home, "docs"), filepath.Join(fx.home, "doclink")); err != nil {
		t.Fatal(err)
	}
	g, err := New([]string{fx.home}, userDeny(t, fx.home, "~/docs/private/**\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := g.List(filepath.Join(fx.home, "docs", "private"))
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := g.List(filepath.Join(fx.home, "doclink", "private"))
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 0 || len(viaLink) != 0 {
		t.Errorf("entries under a denied path must not be listed, directly (%v) or through a symlink (%v)", names(direct), names(viaLink))
	}
}

func TestSearchThroughASymlinkedDirectoryNeverReportsDeniedEntries(t *testing.T) {
	fx := newFixture(t)
	if err := os.Symlink(fx.home, filepath.Join(fx.home, "homelink")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(fx.home, ".ssh", "needle-secret"), []byte("SECRET"))
	put(t, filepath.Join(fx.home, "proj", "needle-ok"), []byte("x"))
	for _, root := range []string{fx.home, filepath.Join(fx.home, "homelink")} {
		ms, _, _, err := searchAll(t, fx.g, root, "ssh", DefaultSearchLimits)
		if err != nil || len(ms) != 0 {
			t.Errorf("root %s: searching for the denied directory's name found %v (%v)", root, rels(ms), err)
		}
		ms, _, _, err = searchAll(t, fx.g, root, "needle", DefaultSearchLimits)
		if err != nil || len(ms) != 1 || !strings.HasSuffix(ms[0].Path, "needle-ok") {
			t.Errorf("root %s: found %v (%v), want only needle-ok", root, rels(ms), err)
		}
	}
}

// Finding 4: an unreadable file inside a denied directory answered
// "permission denied" (403) when reached through a symlink, but 404 directly,
// revealing that it exists.
func TestUnreadableFilesInDeniedDirectoriesLookMissing(t *testing.T) {
	fx := newFixture(t)
	locked := filepath.Join(fx.home, ".ssh", "locked")
	put(t, locked, []byte("SECRET"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })
	if err := os.Symlink(fx.home, filepath.Join(fx.home, "homelink")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(fx.home, ".ssh", "locked"),
		filepath.Join(fx.home, "homelink", ".ssh", "locked"),
		filepath.Join(fx.home, "homelink", ".ssh", "nonexistent"),
		filepath.Join(fx.home, "homelink", "homelink", ".ssh", "locked"),
	} {
		if _, err := readAll(t, fx.g, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want a plain not-found (no 403)", p, err)
		}
	}
	// An unreadable file OUTSIDE any denied place still reports the OS permission error.
	open := filepath.Join(fx.home, "proj", "locked2")
	put(t, open, []byte("x"))
	os.Chmod(open, 0)
	t.Cleanup(func() { os.Chmod(open, 0o600) })
	if _, err := readAll(t, fx.g, open); !errors.Is(err, ErrPermission) {
		t.Errorf("a permission error outside denied places should stay visible to the user, got %v", err)
	}
}

// Finding 4, harder form: the denied directory itself is not searchable, so its
// children cannot be resolved; the deny must still be recognised from the
// directory that can be.
func TestUnsearchableDeniedDirectoryThroughASymlinkLooksMissing(t *testing.T) {
	fx := newFixture(t)
	dir := filepath.Join(fx.home, ".ssh")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := os.Symlink(fx.home, filepath.Join(fx.home, "homelink")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(fx.home, "homelink", ".ssh", "id_ed25519"),
		filepath.Join(fx.home, "homelink", ".ssh", "anything"),
	} {
		if _, err := readAll(t, fx.g, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want not-found", p, err)
		}
	}
}
