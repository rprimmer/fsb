package guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// If $HOME is reached through a symlink, rules built from the unresolved path
// (~/.ssh/ -> /link/.ssh/) do not match the real path the guard compares, so
// requesting the real path directly would walk past them.
func TestRulesBuiltFromASymlinkedHomeStillProtectTheRealPath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realHome := filepath.Join(base, "real-home")
	linkHome := filepath.Join(base, "link-home")
	put(t, filepath.Join(realHome, ".ssh", "id"), []byte("SECRET"))
	put(t, filepath.Join(realHome, "ok.txt"), []byte("fine"))
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatal(err)
	}

	if got := RealPath(linkHome); got != realHome {
		t.Fatalf("RealPath(%s) = %s, want %s", linkHome, got, realHome)
	}
	if got := RealPath(filepath.Join(base, "does-not-exist")); got != filepath.Join(base, "does-not-exist") {
		t.Errorf("an unresolvable path is returned unchanged, got %s", got)
	}

	// This is what main does: build the rules from the resolved home.
	deny, err := rules.Parse(strings.NewReader("~/.ssh/\n"), rules.ParseOptions{Home: RealPath(linkHome)})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]string{linkHome}, deny, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(realHome, ".ssh", "id"), // the real path directly
		filepath.Join(linkHome, ".ssh", "id"), // through the symlink
	} {
		if got, err := readAll(t, g, p); err == nil || !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %q, err %v; want a denial", p, got, err)
		}
	}
	if got, err := readAll(t, g, filepath.Join(linkHome, "ok.txt")); err != nil || got != "fine" {
		t.Errorf("ordinary files stay reachable: %q %v", got, err)
	}

	// Demonstrate the hazard being avoided: rules built from the UNresolved home
	// leave the real path open.
	naive, err := rules.Parse(strings.NewReader("~/.ssh/\n"), rules.ParseOptions{Home: linkHome})
	if err != nil {
		t.Fatal(err)
	}
	gn, err := New([]string{linkHome}, naive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(t, gn, filepath.Join(realHome, ".ssh", "id")); err == nil {
		t.Logf("confirmed: rules from an unresolved home serve the real path (%q); RealPath prevents this", got)
	}
}
