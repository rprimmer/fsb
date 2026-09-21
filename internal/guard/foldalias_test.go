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
