package guard

import (
	"os"
	"path/filepath"
	"testing"
)

// A hard link is another name for the same file, so a path rule cannot see it.
// For the credential locations the guard is told about, a file with more than
// one name is compared by identity with the files in those locations.
func TestHardLinkToAProtectedFileIsRefused(t *testing.T) {
	fx := newFixture(t)
	secret := filepath.Join(fx.home, ".ssh", "id_ed25519")
	if err := os.Link(secret, filepath.Join(fx.home, "proj", "innocent.txt")); err != nil {
		t.Skip("hard links not supported here:", err)
	}
	// Made after the guard was configured, in a protected folder of its own.
	write(t, filepath.Join(fx.home, ".ssh", "later_key"), "LATER-SECRET")
	if err := os.Link(filepath.Join(fx.home, ".ssh", "later_key"), filepath.Join(fx.home, "proj", "later.txt")); err != nil {
		t.Fatal(err)
	}
	// An ordinary file with two names must stay readable.
	write(t, filepath.Join(fx.home, "proj", "a.txt"), "ordinary")
	if err := os.Link(filepath.Join(fx.home, "proj", "a.txt"), filepath.Join(fx.home, "proj", "b.txt")); err != nil {
		t.Fatal(err)
	}

	// Without being told where the credentials are, the limitation stands.
	if f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", "innocent.txt")); err == nil {
		f.Close()
	} else {
		t.Fatalf("precondition: an unprotected guard cannot see hard links: %v", err)
	}

	fx.g.Protect(filepath.Join(fx.home, ".ssh"), filepath.Join(fx.home, ".aws"), filepath.Join(fx.home, "does-not-exist"))
	for _, name := range []string{"innocent.txt", "later.txt"} {
		p := filepath.Join(fx.home, "proj", name)
		if f, _, err := fx.g.Open(p); err == nil {
			f.Close()
			t.Errorf("%s is a second name for a credential file but was served", name)
		}
		if _, err := fx.g.Head(p, 20); err == nil {
			t.Errorf("Head(%s) succeeded", name)
		}
		if got := errorClass(fx.g, p); got != errorClass(fx.g, filepath.Join(fx.home, "proj", "missing.txt")) {
			t.Errorf("%s fails as %q, unlike a missing path", name, got)
		}
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		f, _, err := fx.g.Open(filepath.Join(fx.home, "proj", name))
		if err != nil {
			t.Errorf("%s (an ordinary hard-linked file) must stay reachable: %v", name, err)
			continue
		}
		f.Close()
	}
	// Listings do not show it either.
	es, _ := fx.g.List(filepath.Join(fx.home, "proj"))
	for _, e := range es {
		if e.Name == "innocent.txt" || e.Name == "later.txt" {
			t.Errorf("listing shows %s", e.Name)
		}
	}
}
