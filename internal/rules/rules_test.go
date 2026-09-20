package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const home = "/Users/u"

func mustParse(t *testing.T, text string, negation bool) *Set {
	t.Helper()
	s, err := Parse(strings.NewReader(text), ParseOptions{Home: home, AllowNegation: negation})
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return s
}

func TestMatch(t *testing.T) {
	tests := []struct {
		name    string
		rules   string
		path    string
		isDir   bool
		matched bool
	}{
		// Home-anchored directory rule.
		{"home dir itself", "~/.ssh/", "/Users/u/.ssh", true, true},
		{"home dir file inside", "~/.ssh/", "/Users/u/.ssh/id_rsa", false, true},
		{"home dir deep inside", "~/.ssh/", "/Users/u/.ssh/a/b/c", false, true},
		{"dir-only rule ignores a file of that name", "~/.ssh/", "/Users/u/.ssh", false, false},
		{"prefix-sibling not matched", "~/.ssh/", "/Users/u/.sshx/id", false, false},
		{"anchored rule not matched deeper", "~/.ssh/", "/Users/u/foo/.ssh/id", false, false},
		{"case-insensitive", "~/.ssh/", "/Users/u/.SSH/id_rsa", false, true},
		{"case-insensitive mixed", "~/Library/Keychains/", "/users/U/library/KEYCHAINS/login", false, true},
		{"other user's home not matched", "~/.ssh/", "/Users/other/.ssh/id", false, false},
		{"tilde file rule", "~/.netrc", "/Users/u/.netrc", false, true},
		{"spaces in path", "~/Library/Application Support/Google/Chrome/", "/Users/u/Library/Application Support/Google/Chrome/Default/Cookies", false, true},

		// Unanchored.
		{"basename any depth", ".env", "/Users/u/proj/.env", false, true},
		{"basename not substring", ".env", "/Users/u/proj/.envrc", false, false},
		{"basename dir ancestor", ".env", "/Users/u/.env/x", false, true},
		{"star suffix", "*.pem", "/a/b/key.pem", false, true},
		{"star suffix not partial", "*.pem", "/a/b/key.pem.txt", false, false},
		{"star does not cross slash", "a*b", "/x/a/b", false, false},
		{"dot star", ".env.*", "/x/.env.local", false, true},
		{"question mark", "?.txt", "/x/a.txt", false, true},
		{"question mark exactly one", "?.txt", "/x/ab.txt", false, false},
		{"char class", "[ab]c", "/x/bc", false, true},
		{"char class miss", "[ab]c", "/x/cc", false, false},
		{"negated char class", "[!ab]c", "/x/cc", false, true},
		{"leading double star", "**/secrets/", "/x/y/secrets/k", false, true},
		{"trailing double star", "/x/logs/**", "/x/logs/a/b.log", false, true},
		{"middle double star zero dirs", "/x/a/**/b", "/x/a/b", false, true},
		{"middle double star many dirs", "/x/a/**/b", "/x/a/p/q/b", false, true},
		{"escaped star", `a\*b`, "/x/a*b", false, true},
		{"escaped star no wildcard", `a\*b`, "/x/aXb", false, false},

		// Base-relative rule (contains a slash, not absolute).
		{"base-relative match", "Library/Keychains/", "/Users/u/Library/Keychains/login", false, true},
		{"base-relative not deeper", "Library/Keychains/", "/Users/u/other/Library/Keychains/x", false, false},

		// Absolute rule.
		{"absolute", "/etc/secret", "/etc/secret", false, true},
		{"absolute not elsewhere", "/etc/secret", "/tmp/etc/secret", false, false},

		// Regexp metacharacters in the home path or pattern are literal.
		{"dot is literal", "a.b", "/x/aXb", false, false},
		{"plus is literal", "a+b", "/x/a+b", false, true},
		{"paren is literal", "(x)", "/y/(x)", false, true},

		// Unicode: NFC pattern vs NFD path, and vice versa.
		{"nfc pattern nfd path", "~/café/", "/Users/u/café/menu", false, true},
		{"nfd pattern nfc path", "~/café/", "/Users/u/café/menu", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustParse(t, tc.rules, false)
			got := s.Match(tc.path, tc.isDir)
			if got.Matched != tc.matched {
				t.Fatalf("rules %q path %q isDir=%v: matched=%v, want %v", tc.rules, tc.path, tc.isDir, got.Matched, tc.matched)
			}
			if got.Matched && got.Rule != strings.TrimSpace(tc.rules) {
				t.Fatalf("rule text = %q, want %q", got.Rule, tc.rules)
			}
		})
	}
}

func TestRegexMetacharsInHomePath(t *testing.T) {
	s, err := Parse(strings.NewReader("~/.ssh/"), ParseOptions{Home: "/Users/a.b[c]+"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Match("/Users/a.b[c]+/.ssh/id", false).Matched {
		t.Error("literal home path with metacharacters should match")
	}
	if s.Match("/Users/aXbc/.ssh/id", false).Matched {
		t.Error("home path metacharacters must not act as a pattern")
	}
}

func TestNegationAndOrdering(t *testing.T) {
	s := mustParse(t, "*.log\n!keep.log\n", true)
	if !s.Match("/x/a.log", false).Matched {
		t.Error("a.log should be excluded")
	}
	if s.Match("/x/keep.log", false).Matched {
		t.Error("keep.log should be re-included by negation")
	}

	// Last match wins: re-excluding after a negation.
	s = mustParse(t, "*.log\n!keep.log\nkeep.log\n", true)
	if !s.Match("/x/keep.log", false).Matched {
		t.Error("later rule should win")
	}
}

func TestCannotReincludeUnderExcludedDirectory(t *testing.T) {
	s := mustParse(t, "build/\n!build/keep.txt\n!keep.txt\n", true)
	if !s.Match("/x/build/keep.txt", false).Matched {
		t.Error("a file under an excluded directory must stay excluded despite negation")
	}
}

func TestDenyRejectsNegation(t *testing.T) {
	_, err := Parse(strings.NewReader("~/.ssh/\n!~/.ssh/id_pub\n"), ParseOptions{Home: home})
	if err == nil {
		t.Fatal("negation must be rejected when AllowNegation is false")
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"/", "!", `trailing\`, "[a-", "a[[:bogus:]]"} {
		// "[a-" is a literal '[' (unterminated class); it must still parse.
		_, err := Parse(strings.NewReader(bad), ParseOptions{Home: home, AllowNegation: true})
		switch bad {
		case "[a-":
			if err != nil {
				t.Errorf("%q should parse as a literal: %v", bad, err)
			}
		default:
			if err == nil {
				t.Errorf("%q should be a parse error", bad)
			}
		}
	}
}

func TestCommentsAndBlankLines(t *testing.T) {
	s := mustParse(t, "# comment\n\n   \n  # indented comment\n.env\n", false)
	if got := s.Texts(); len(got) != 1 || got[0] != ".env" {
		t.Fatalf("Texts = %q, want [.env]", got)
	}
}

func TestMatchBelow(t *testing.T) {
	s := mustParse(t, "node_modules/\n", false)
	if !s.MatchBelow("/x", "/x/node_modules", true).Matched {
		t.Error("entry named node_modules should be hidden when listing /x")
	}
	if s.MatchBelow("/x/node_modules", "/x/node_modules/pkg", true).Matched {
		t.Error("listing inside a hidden dir should show its contents")
	}
	if !s.Match("/x/node_modules/pkg", true).Matched {
		t.Error("Match (all ancestors) should still exclude the contents")
	}
}

func TestNilSetMatchesNothing(t *testing.T) {
	var s *Set
	if s.Match("/x", true).Matched {
		t.Error("nil set must match nothing")
	}
}

func TestDenyTemplateActivatesExactlyCore(t *testing.T) {
	s, err := Parse(strings.NewReader(DenyTemplate()), ParseOptions{Home: home})
	if err != nil {
		t.Fatalf("template must parse: %v", err)
	}
	got := s.Texts()
	if len(got) != len(CoreDeny) {
		t.Fatalf("active rules = %v, want exactly the core set %v", got, CoreDeny)
	}
	for i := range got {
		if got[i] != CoreDeny[i] {
			t.Fatalf("rule %d = %q, want %q", i, got[i], CoreDeny[i])
		}
	}
	if missing := CoreMissing(s); len(missing) != 0 {
		t.Fatalf("template should not report missing core rules: %v", missing)
	}
}

func TestIgnoreTemplateParses(t *testing.T) {
	if _, err := Parse(strings.NewReader(IgnoreTemplate()), ParseOptions{Home: home, AllowNegation: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCoreDenyCoversKnownSecrets(t *testing.T) {
	s, err := Parse(strings.NewReader(strings.Join(CoreDeny, "\n")), ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/Users/u/.ssh/id_ed25519",
		"/Users/u/.aws/credentials",
		"/Users/u/.gnupg/private-keys-v1.d/k",
		"/Users/u/.netrc",
		"/Users/u/Library/Keychains/login.keychain-db",
		"/Users/u/Library/Application Support/Google/Chrome/Default/Login Data",
	} {
		if !s.Match(p, false).Matched {
			t.Errorf("core deny should cover %s", p)
		}
	}
}

func TestLoadDeny(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file uses core rules", func(t *testing.T) {
		s, missing, err := LoadDeny(filepath.Join(dir, "nope"), home)
		if err != nil || len(missing) != 0 {
			t.Fatalf("err=%v missing=%v", err, missing)
		}
		if !s.Match("/Users/u/.ssh/id", false).Matched {
			t.Error("built-in core rules must apply when the file is missing")
		}
	})

	t.Run("file is authoritative and weakening is reported", func(t *testing.T) {
		p := filepath.Join(dir, "deny")
		os.WriteFile(p, []byte("~/.aws/\n# ~/.ssh/\n"), 0o600)
		s, missing, err := LoadDeny(p, home)
		if err != nil {
			t.Fatal(err)
		}
		if s.Match("/Users/u/.ssh/id", false).Matched {
			t.Error("a core rule removed from the file should no longer apply")
		}
		found := false
		for _, m := range missing {
			if m == "~/.ssh/" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing = %v, should include ~/.ssh/", missing)
		}
	})

	t.Run("malformed file fails closed", func(t *testing.T) {
		p := filepath.Join(dir, "bad")
		os.WriteFile(p, []byte("~/.ssh/\n!~/.ssh/id_pub\n"), 0o600)
		if _, _, err := LoadDeny(p, home); err == nil {
			t.Fatal("deny file with negation must be an error")
		}
	})
}

func TestLoadIgnore(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadIgnore(filepath.Join(dir, "nope"), home)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Match("/x/.DS_Store", false).Matched || !s.Match("/x/node_modules", true).Matched {
		t.Error("default ignore rules should apply when the file is missing")
	}
}

func FuzzParseAndMatch(f *testing.F) {
	for _, seed := range []string{"~/.ssh/", "*.pem", "**/a/**/b", "[a-z]?", `a\*`, "!x", "/", "["} {
		f.Add(seed, "/Users/u/.ssh/id")
	}
	f.Fuzz(func(t *testing.T, pattern, path string) {
		s, err := Parse(strings.NewReader(pattern), ParseOptions{Home: home, AllowNegation: true})
		if err != nil {
			return
		}
		s.Match(path, true) // must not panic
	})
}
