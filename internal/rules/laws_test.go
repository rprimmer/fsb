package rules

import (
	"math/rand"
	"strings"
	"testing"
)

// Property tests for the laws in algebra/ (section "Rules"). Each test names the
// law it checks. They use a fixed seed so a failure is reproducible.

var (
	lawRules = []string{
		"~/.ssh", "~/.aws/", "*.key", "*.pem", ".env", ".env.*", "cache/", "/tmp/secret",
		"**/node_modules", "build/**", "a?c", "[abc]x", "~/Library/Keychains", "notes*.txt",
	}
	lawPaths = []string{
		"/Users/me", "/Users/me/.ssh", "/Users/me/.ssh/id", "/Users/me/.ßh/id", "/Users/me/.SSH",
		"/Users/me/.aws/credentials", "/Users/me/p/a.key", "/Users/me/p/A.KEY", "/Users/me/p/a.Key",
		"/Users/me/p/cache", "/Users/me/p/cache/x", "/Users/me/x/.env", "/Users/me/x/.env.local",
		"/tmp/secret", "/tmp/secret/y", "/Users/me/w/node_modules/pkg/i.js", "/Users/me/w/build/out/a.o",
		"/Users/me/abc", "/Users/me/bx", "/Users/me/notes 2026.txt", "/Users/me/Library/Keychains/login.keychain-db",
		"/Users/me/Documents/readme.md", "/Users/me/Café/a.pem", "/Users/me/Café/a.pem", "/Users/me/p/id.pem ",
	}
	lawAliases = map[rune]string{'s': "ſ", 'k': "K", 'K': "K"}
)

func mustSet(t *testing.T, lines []string, neg bool) *Set {
	t.Helper()
	s, err := Parse(strings.NewReader(strings.Join(lines, "\n")), ParseOptions{Home: "/Users/me", AllowNegation: neg})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pick(r *rand.Rand, from []string) []string {
	var out []string
	for _, x := range from {
		if r.Intn(2) == 0 {
			out = append(out, x)
		}
	}
	return out
}

// Law "deny monotonicity": adding rules to a rule set without negation never
// makes a path accessible. Law "order independence": without negation the
// order of rules is irrelevant.
func TestLawDenyIsMonotoneAndOrderIndependent(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 60; i++ {
		base := pick(r, lawRules)
		extra := append(append([]string{}, base...), pick(r, lawRules)...)
		shuffled := append([]string{}, extra...)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		sb, se, ss := mustSet(t, base, false), mustSet(t, extra, false), mustSet(t, shuffled, false)
		for _, p := range lawPaths {
			for _, dir := range []bool{false, true} {
				if sb.Match(p, dir).Matched && !se.Match(p, dir).Matched {
					t.Fatalf("monotonicity: %q denied by %v but not by its superset", p, base)
				}
				if se.Match(p, dir).Matched != ss.Match(p, dir).Matched {
					t.Fatalf("order independence: %q differs after shuffling %v", p, extra)
				}
			}
		}
	}
}

// Law "ancestor closure": if a path is excluded, so is everything beneath it.
// Holds even with negation in the file, because an excluded ancestor cannot be
// re-included (as in git).
func TestLawAncestorClosure(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 40; i++ {
		lines := pick(r, lawRules)
		for _, l := range pick(r, lawRules) {
			lines = append(lines, "!"+l)
		}
		for _, neg := range []bool{false, true} {
			set := lines
			if !neg {
				set = nil
				for _, l := range lines {
					if !strings.HasPrefix(l, "!") {
						set = append(set, l)
					}
				}
			}
			s := mustSet(t, set, neg)
			for _, p := range lawPaths {
				if s.Match(p, true).Matched {
					for _, child := range []string{p + "/x", p + "/x/y.txt"} {
						if !s.Match(child, false).Matched {
							t.Fatalf("closure: %q is excluded but %q is not (rules %v)", p, child, set)
						}
					}
				}
			}
		}
	}
}

// Law "fold invariance": a path is excluded exactly when any spelling that
// APFS treats as the same name is. Fold is idempotent, and Match sees only the
// folded path.
func TestLawMatchDependsOnlyOnTheFoldedPath(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	s := mustSet(t, lawRules, false)
	for _, p := range lawPaths {
		f := Fold(p)
		if Fold(f) != f {
			t.Errorf("Fold is not idempotent on %q", p)
		}
		if s.Match(p, false) != s.Match(f, false) && s.Match(p, false).Matched != s.Match(f, false).Matched {
			t.Errorf("Match(%q) differs from Match(Fold(%q))", p, p)
		}
	}
	for i := 0; i < 500; i++ {
		p := lawPaths[r.Intn(len(lawPaths))]
		var b strings.Builder
		for _, c := range p {
			switch {
			case r.Intn(3) == 0 && lawAliases[c] != "":
				b.WriteString(lawAliases[c])
			case r.Intn(3) == 0:
				b.WriteString(strings.ToUpper(string(c)))
			default:
				b.WriteRune(c)
			}
		}
		q := b.String()
		if Fold(p) == Fold(q) && s.Match(p, false).Matched != s.Match(q, false).Matched {
			t.Fatalf("%q and %q are the same name to APFS but are treated differently", p, q)
		}
	}
}

// Law "narrowing": ignoring ancestors (MatchBelow) can only remove matches.
func TestLawMatchBelowNeverAddsMatches(t *testing.T) {
	s := mustSet(t, lawRules, false)
	for _, p := range lawPaths {
		for _, from := range []string{"/", "/Users", "/Users/me", "/Users/me/p"} {
			if s.MatchBelow(from, p, false).Matched && !s.Match(p, false).Matched {
				t.Errorf("MatchBelow(%q,%q) matched but Match did not", from, p)
			}
		}
	}
}

// Law "no negation in deny": Parse refuses "!" unless the caller allows it.
func TestLawDenyRefusesNegation(t *testing.T) {
	if _, err := Parse(strings.NewReader("!*.key"), ParseOptions{Home: "/Users/me"}); err == nil {
		t.Error("negation accepted in a deny file")
	}
}

// Law "single component": a pattern that contains no "/" and no "**" can only
// match within one path component, never across a "/".
func TestLawSlashlessPatternsStayWithinOneComponent(t *testing.T) {
	for _, pat := range []string{"x[!a]y", "x[^a]y", "x?y", "x*y", "[!a]", "x[!a]*"} {
		s := mustSet(t, []string{pat}, false)
		for _, p := range []string{"/Users/me/x/y", "/Users/me/xx/y", "/Users/me/x/zy", "/Users/me/p/x/", "/Users/me/x/"} {
			if s.Match(p, false).Matched {
				// Only matches whose text lies inside one component are legitimate.
				comps := strings.Split(strings.TrimPrefix(p, "/"), "/")
				ok := false
				for _, c := range comps {
					if c != "" && mustSet(t, []string{pat}, false).Match("/"+c, false).Matched {
						ok = true
					}
				}
				if !ok {
					t.Errorf("pattern %q matched %q across a slash", pat, p)
				}
			}
		}
	}
}

func TestSlashInsideAClassIsAParseError(t *testing.T) {
	if _, err := Parse(strings.NewReader("a[/]b"), ParseOptions{Home: "/Users/me"}); err == nil {
		t.Error("a class containing / was accepted")
	}
}
