package rules

import (
	"strings"
	"testing"
)

// Every alias below was measured on APFS (case-insensitive): the name on the
// left opens the file named on the right.
var apfsAliases = [][2]string{
	{"ß", "ss"}, {"ẞ", "ss"}, {"ſ", "s"}, {"K", "k"}, // Kelvin sign
	{"ﬀ", "ff"}, {"ﬁ", "fi"}, {"ﬂ", "fl"}, {"ﬃ", "ffi"}, {"ﬄ", "ffl"}, {"ﬅ", "st"}, {"ﬆ", "st"},
}

func TestFoldMatchesTheAliasesAPFSTreatsAsEqual(t *testing.T) {
	for _, a := range apfsAliases {
		if Fold(a[0]) != Fold(a[1]) {
			t.Errorf("Fold(%q) = %q, Fold(%q) = %q: APFS treats these as one name", a[0], Fold(a[0]), a[1], Fold(a[1]))
		}
	}
	if Fold("Café") != Fold("Café") || Fold("ÉCOLE") != Fold("école") {
		t.Error("normalization and case must not matter")
	}
	if Fold("/A/b") != "/a/b" {
		t.Error("ASCII fast path")
	}
}

func TestRulesCannotBeDodgedWithFoldingAliases(t *testing.T) {
	deny, err := Parse(strings.NewReader("~/.ssh\n*.key\n.env\n*.pem\n/private/etc/skey\n"), ParseOptions{Home: "/Users/me"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/Users/me/.ßh/id", "/Users/me/.ſſh", "/Users/me/.SSH/x", "/Users/me/.ẞh",
		"/Users/me/a.Key", "/Users/me/a.KEY", "/Users/me/x/.ENV", "/Users/me/p.PEM",
		"/private/etc/ſkey", // rule spelled with plain letters, path with the long s
	} {
		if !deny.Match(p, false).Matched {
			t.Errorf("%q was not denied", p)
		}
	}
	// A rule spelled with an alias matches the plain spelling too.
	d2, _ := Parse(strings.NewReader("~/.ßh\n"), ParseOptions{Home: "/Users/me"})
	if !d2.Match("/Users/me/.ssh/id", false).Matched {
		t.Error("a rule written with ß must match .ssh")
	}
	// Unrelated names still pass.
	for _, p := range []string{"/Users/me/.sh", "/Users/me/key", "/Users/me/s.keyx"} {
		if deny.Match(p, false).Matched {
			t.Errorf("%q must not be denied", p)
		}
	}
}

func TestCoreMissingComparesRulesAsNamesAreCompared(t *testing.T) {
	all := strings.Join(CoreDeny, "\n")
	s, err := Parse(strings.NewReader(all), ParseOptions{Home: "/Users/me"})
	if err != nil {
		t.Fatal(err)
	}
	if m := CoreMissing(s); len(m) != 0 {
		t.Fatalf("missing with the full set: %v", m)
	}
	// The same rules spelled with other case are still the core rules.
	upper, _ := Parse(strings.NewReader(strings.ToUpper(all)), ParseOptions{Home: "/Users/me"})
	if m := CoreMissing(upper); len(m) != 0 {
		t.Errorf("case-changed core rules reported missing: %v", m)
	}
	// A different rule (here: also matching a file) is not the core rule.
	weak, _ := Parse(strings.NewReader(strings.Replace(all, ".aws/", ".aws", 1)), ParseOptions{Home: "/Users/me"})
	if m := CoreMissing(weak); len(m) != 1 {
		t.Errorf("weakened rule: missing = %v, want exactly one", m)
	}
}
