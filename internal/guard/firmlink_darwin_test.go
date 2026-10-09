package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// alias is the same location named through the data volume's firmlink.
func alias(p string) string { return dataVolume + p }

func TestCanonPath(t *testing.T) {
	for in, want := range map[string]string{
		"/System/Volumes/Data/Users/me/.ssh/id":                   "/Users/me/.ssh/id",
		"/System/Volumes/Data/Users":                              "/Users",
		"/system/volumes/DATA/USERS/me":                           "/Users/me", // case-insensitive match; the firmlink's own spelling is used
		"/System/Volumes/Data/private/tmp/x":                      "/private/tmp/x",
		"/System/Volumes/Data/Applications/Foo.app":               "/Applications/Foo.app",
		"/System/Volumes/Data/usr/local/bin":                      "/usr/local/bin",
		"/System/Volumes/Data/System/Library/Caches/x":            "/System/Library/Caches/x",
		"/System/Volumes/Data/System/Library/Other":               "/System/Volumes/Data/System/Library/Other", // not a firmlink
		"/System/Volumes/Data/nonexistent":                        "/System/Volumes/Data/nonexistent",
		"/System/Volumes/Data":                                    "/System/Volumes/Data",
		"/System/Volumes/DataX/Users/me":                          "/System/Volumes/DataX/Users/me", // a different name, not the volume
		"/Users/me/.ssh":                                          "/Users/me/.ssh",
		"/":                                                       "/",
		"/System/Volumes/Data/Users/me/System/Volumes/Data/Users": "/Users/me/System/Volumes/Data/Users", // only the leading alias is rewritten
	} {
		if got := canonPath(in); got != want {
			t.Errorf("canonPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirmlinkAliasesCannotDodgeDenyRules(t *testing.T) {
	fx := newFixture(t)
	secret := filepath.Join(fx.home, ".ssh", "id_ed25519")
	variants := []string{
		alias(secret),
		"/system/volumes/DATA" + secret,
		alias(fx.home) + "/proj/../.ssh/id_ed25519",
		alias(fx.home) + "//.ssh//id_ed25519",
		alias(filepath.Join(fx.home, ".SSH", "id_ed25519")),
	}
	for _, p := range variants {
		if got, err := readAll(t, fx.g, p); err == nil {
			t.Errorf("read a denied file through the alias %s: %q", p, got)
		}
		if _, err := fx.g.Head(p, 100); !errors.Is(err, ErrNotFound) {
			t.Errorf("Head %s: %v", p, err)
		}
		if _, err := fx.g.Meta(p, true); !errors.Is(err, ErrNotFound) {
			t.Errorf("Meta %s: %v", p, err)
		}
		if _, _, _, err := fx.g.OpenImage(p); !errors.Is(err, ErrNotFound) {
			t.Errorf("OpenImage %s: %v", p, err)
		}
	}
	if _, err := fx.g.List(alias(filepath.Join(fx.home, ".ssh"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("listing the denied directory through its alias: %v", err)
	}
	if _, _, _, err := searchAll(t, fx.g, alias(filepath.Join(fx.home, ".ssh")), "id", DefaultSearchLimits); !errors.Is(err, ErrNotFound) {
		t.Errorf("searching the denied directory through its alias: %v", err)
	}
}

// The reported bug: with a root of "/" the alias is inside the root, and
// used to walk straight past the deny rules.
func TestRootOfSlashHonoursDenyRulesThroughAliases(t *testing.T) {
	fx := newFixture(t)
	g, err := New([]string{"/"}, fx.g.deny, fx.g.hide)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(fx.home, ".ssh", "id_ed25519")
	for _, p := range []string{secret, alias(secret), "/SYSTEM/volumes/data" + secret} {
		if got, err := readAll(t, g, p); err == nil {
			t.Errorf("root / served a denied file at %s: %q", p, got)
		}
	}
	// Ordinary files are still reachable, by either name.
	for _, p := range []string{filepath.Join(fx.home, "proj", "hello.txt"), alias(filepath.Join(fx.home, "proj", "hello.txt"))} {
		if got, err := readAll(t, g, p); err != nil || got != "hello" {
			t.Errorf("%s: %q, %v", p, got, err)
		}
	}
	// A listing through the alias omits the denied directory.
	es, err := g.List(alias(fx.home))
	if err != nil {
		t.Fatal(err)
	}
	if contains(names(es), ".ssh") {
		t.Errorf("listing through the alias exposes .ssh: %v", names(es))
	}
	// Search through the alias neither reports nor enters it.
	put(t, filepath.Join(fx.home, ".ssh", "needle-secret"), []byte("SECRET"))
	put(t, filepath.Join(fx.home, "proj", "needle-ok"), []byte("x"))
	var found []string
	_, _, err = g.Search(context.Background(), alias(fx.home), "needle", DefaultSearchLimits, func(m SearchMatch) error {
		found = append(found, m.Rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.HasSuffix(found[0], "needle-ok") {
		t.Errorf("search through the alias found %v, want only proj/needle-ok", found)
	}
}

func TestSymlinkToAnAliasOfADeniedFileIsDenied(t *testing.T) {
	fx := newFixture(t)
	link := filepath.Join(fx.home, "proj", "sneaky")
	if err := os.Symlink(alias(filepath.Join(fx.home, ".ssh", "id_ed25519")), link); err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(t, fx.g, link); err == nil {
		t.Fatalf("read a denied file through a symlink to its alias: %q", got)
	}
	es, err := fx.g.List(filepath.Join(fx.home, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(names(es), "sneaky") {
		t.Errorf("a symlink to an alias of a denied file must not be listed: %v", names(es))
	}
}

func TestAnAliasOfAnAllowedFileIsStillServedWithinTheRoots(t *testing.T) {
	fx := newFixture(t)
	got, err := readAll(t, fx.g, alias(filepath.Join(fx.home, "proj", "hello.txt")))
	if err != nil || got != "hello" {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestRootGivenAsAnAliasIsCanonicalized(t *testing.T) {
	fx := newFixture(t)
	g, err := New([]string{alias(fx.home)}, fx.g.deny, fx.g.hide)
	if err != nil {
		t.Fatal(err)
	}
	if roots := g.Roots(); len(roots) != 1 || roots[0] != fx.home {
		t.Errorf("roots = %v, want [%s]", roots, fx.home)
	}
	if _, err := readAll(t, g, filepath.Join(fx.home, ".ssh", "id_ed25519")); err == nil {
		t.Error("a root given as an alias must still enforce the deny rules")
	}
}

// Under --root /, the special root entries are not listed: opening them is
// refused (throughSpecialRoot), so a listing must not offer them.
func TestRootListingLeavesOutTheSpecialRootEntries(t *testing.T) {
	deny, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{})
	hide, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{})
	g, err := New([]string{"/"}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := g.List("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	for _, special := range []string{".nofollow", ".resolve", ".vol"} {
		if slices.Contains(names, special) {
			t.Errorf("listing / shows %s", special)
		}
	}
	if !slices.Contains(names, "Users") {
		t.Fatalf("listing / does not show Users: %v", names)
	}
}

// A root given through a special root entry would serve nothing; it is
// refused when fsb starts, with a message.
func TestARootThroughASpecialRootEntryIsRefused(t *testing.T) {
	deny, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{})
	home, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := New([]string{"/.nofollow" + home}, deny, deny); err == nil {
		t.Fatal("a root under /.nofollow was accepted")
	}
	if _, err := New([]string{home}, deny, deny); err != nil {
		t.Fatalf("the same folder, spelled normally: %v", err)
	}
}
