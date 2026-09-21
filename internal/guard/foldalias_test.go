package guard

import (
	"os"
	"path/filepath"
	"testing"
)

// APFS (case-insensitive) compares names with Unicode *full* case folding, so
// "ß" and "ss" name the same file. Rules that fold differently can be dodged by
// spelling a denied name with such a character.
func TestDenyRulesSurviveFullCaseFoldingAliases(t *testing.T) {
	fx := newFixture(t)
	probe := filepath.Join(fx.home, ".ssh")
	alias := filepath.Join(fx.home, ".ßh")
	if _, err := os.Stat(filepath.Join(alias, "id_ed25519")); err != nil {
		t.Skip("this file system does not fold ß to ss")
	}
	for _, p := range []string{
		filepath.Join(alias, "id_ed25519"),
		alias,
		filepath.Join(fx.home, ".SSH", "id_ed25519"),
		filepath.Join(fx.home, ".ſſh", "id_ed25519"),
	} {
		if f, _, err := fx.g.Open(p); err == nil {
			f.Close()
			t.Errorf("Open(%q) succeeded although %q is denied", p, probe)
		}
		if _, err := fx.g.Head(p, 10); err == nil {
			t.Errorf("Head(%q) succeeded", p)
		}
	}
	// And a listing of the parent must not expose it under either spelling.
	es, err := fx.g.List(fx.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Name == ".ssh" || e.Name == ".ßh" {
			t.Errorf("listing shows %q", e.Name)
		}
	}
}

// The firmlink prefix and the names inside it are matched the way APFS matches
// names, so spelling "System" with a long s or the "st" ligature must not stop
// the alias from being reduced (and so must not dodge a deny rule under a root
// of "/").
func TestRootOfSlashHonoursDenyRulesThroughFoldedFirmlinkSpellings(t *testing.T) {
	fx := newFixture(t)
	g, err := New([]string{"/"}, fx.g.deny, fx.g.hide)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(fx.home, ".ssh", "id_ed25519")
	if _, err := os.Stat("/ſystem/Volumes/Data" + secret); err != nil {
		t.Skip("this file system does not fold the long s")
	}
	for _, p := range []string{
		"/ſystem/Volumes/Data" + secret,
		"/Syﬅem/Volumes/Data" + secret,
		"/System/Volumeſ/Data" + secret,
		"/System/Volumes/Daﬅa" + secret,
	} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue // this spelling does not name the file here
		}
		if f, _, err := g.Open(p); err == nil {
			f.Close()
			t.Errorf("Open(%q) served a denied file", p)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"/ſystem/Volumes/Data/Users/me", "/Users/me"},
		{"/System/Volumes/Data/Uſers/me", "/Users/me"},
		{"/Syﬅem/Volumes/Data/Users", "/Users"},
	} {
		if got := canonPath(c.in); got != c.want {
			t.Errorf("canonPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
