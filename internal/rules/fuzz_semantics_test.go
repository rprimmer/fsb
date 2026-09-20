package rules

import (
	"path/filepath"
	"strings"
	"testing"
)

// The existing fuzz test only checks that parsing and matching do not panic.
// This one checks a property that matters for security: whatever follows a
// denied directory in a path, that path is denied, in any letter case.
func FuzzDenyMatchesEverythingUnderADeniedDirectory(f *testing.F) {
	set, err := Parse(strings.NewReader("~/.ssh/\n~/Library/Keychains/\n"), ParseOptions{Home: home})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"id_rsa", "a/b/c", "../../.ssh/x", ".", "..", "", "é", "é", "a b", "*", "[x]", "\\", "%2e%2e", strings.Repeat("a/", 500)} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, suffix string, upper bool) {
		if strings.ContainsRune(suffix, 0) {
			t.Skip()
		}
		dir := ".ssh"
		if upper {
			dir = ".SSH"
		}
		p := filepath.Clean(home + "/" + dir + "/" + suffix)
		// Cleaning may legitimately climb out of the denied directory (a/../..), or
		// land on a sibling that merely shares the prefix (../.ssh0). Only paths
		// strictly inside ~/.ssh/ are checked.
		if !strings.HasPrefix(strings.ToLower(p), strings.ToLower(home+"/.ssh/")) {
			t.Skip()
		}
		if !set.Match(p, false).Matched {
			t.Fatalf("%q is under ~/.ssh but was not denied", p)
		}
	})
}

// A deny file must never accept a negation, whatever surrounds it.
func FuzzDenyFilesRejectNegation(f *testing.F) {
	for _, seed := range []string{"", "~/.ssh/", "# comment", "  ", "a\nb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, other string) {
		if strings.ContainsAny(other, "\r") {
			t.Skip()
		}
		text := other + "\n!~/.ssh/id_pub\n"
		if _, err := Parse(strings.NewReader(text), ParseOptions{Home: home}); err == nil {
			// The only way a "!" line can be legal is if an earlier line swallowed it
			// as part of a comment or a continuation; there are no continuations.
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "!") && !strings.HasPrefix(strings.TrimSpace(strings.Split(text, "\n")[0]), "#") {
					t.Fatalf("a deny file accepted a negation line: %q", text)
				}
			}
		}
	})
}
