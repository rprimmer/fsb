package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// With a root of "/", paths such as /dev/fd/N name the server's own open file
// descriptors. They are not part of any directory tree that rules describe, so
// they must be refused: they can reach a file the process holds open under a
// name that no rule saw.
func TestDescriptorPathsAreRefusedUnderARootOfSlash(t *testing.T) {
	fx := newFixture(t)
	g, err := New([]string{"/"}, fx.g.deny, fx.g.hide)
	if err != nil {
		t.Fatal(err)
	}
	// A denied file that the process happens to hold open (a stand-in for any
	// file the server has open at the moment of the request).
	secret, err := os.Open(filepath.Join(fx.home, ".ssh", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()
	for _, p := range []string{
		fmt.Sprintf("/dev/fd/%d", secret.Fd()),
		fmt.Sprintf("/dev/fd/%d/", secret.Fd()),
		fmt.Sprintf("/dev//fd/%d", secret.Fd()),
		fmt.Sprintf("/private/dev/fd/%d", secret.Fd()),
		"/dev/stdin", "/dev/stdout", "/dev/stderr",
	} {
		if b, err := readAll(t, g, p); err == nil && strings.Contains(b, "SECRET") {
			t.Errorf("Open(%q) served a denied file through the process's own descriptor", p)
		}
		if f, _, err := g.Open(p); err == nil {
			f.Close()
			t.Errorf("Open(%q) succeeded; descriptor paths must be refused", p)
		}
	}
	_ = rules.CoreDeny
}
