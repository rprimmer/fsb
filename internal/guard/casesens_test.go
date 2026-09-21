package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

// On a case-sensitive volume "Public" and "public" are different folders. A
// root of one must not admit the other, although the rules (which may
// over-match) treat the two spellings as one name.
func TestRootContainmentIsByIdentityNotSpelling(t *testing.T) {
	vol := os.Getenv("FSB_CASE_SENSITIVE_VOLUME")
	if vol == "" {
		t.Skip("set FSB_CASE_SENSITIVE_VOLUME to a directory on a case-sensitive volume")
	}
	base := filepath.Join(vol, "fsb-roots-test")
	os.RemoveAll(base)
	defer os.RemoveAll(base)
	write(t, filepath.Join(base, "Public", "a.txt"), "OPEN")
	write(t, filepath.Join(base, "public", "b.txt"), "SIBLING")
	deny, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: base})
	g, err := New([]string{filepath.Join(base, "Public")}, deny, deny)
	if err != nil {
		t.Fatal(err)
	}
	if f, _, err := g.Open(filepath.Join(base, "Public", "a.txt")); err != nil {
		t.Fatalf("the root's own file: %v", err)
	} else {
		f.Close()
	}
	if f, _, err := g.Open(filepath.Join(base, "public", "b.txt")); err == nil {
		f.Close()
		t.Error("a different folder that differs from the root only in case was served")
	}
	if es, err := g.List(filepath.Join(base, "public")); err == nil {
		t.Errorf("listed the sibling folder: %v", es)
	}
}
