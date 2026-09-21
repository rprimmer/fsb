package rules

import (
	"strings"
	"testing"
)

// A rule file is written by a person in an editor. Whatever the editor adds or
// the person expects, a rule must either work as written or fail loudly: never
// load silently and do nothing (a deny rule that does nothing is the worst
// outcome).

func denies(t *testing.T, text, path string) bool {
	t.Helper()
	s, err := Parse(strings.NewReader(text), ParseOptions{Home: "/Users/me"})
	if err != nil {
		return false // refusing to load is loud, so not a silent failure
	}
	return s.Match(path, false).Matched
}

func loudOrWorks(t *testing.T, name, text, path string) {
	t.Helper()
	s, err := Parse(strings.NewReader(text), ParseOptions{Home: "/Users/me"})
	if err != nil {
		return
	}
	if !s.Match(path, false).Matched {
		t.Errorf("%s: the rule file loaded without error but %s is not denied\n%q", name, path, text)
	}
}

func TestRuleFilesEditorsProduceWorkOrFailLoudly(t *testing.T) {
	const target = "/Users/me/.ssh/id_rsa"
	for name, text := range map[string]string{
		"utf-8 BOM":                  "\xef\xbb\xbf.ssh/\n",
		"BOM then comment then rule": "\xef\xbb\xbf# keys\n.ssh/\n",
		"CRLF line endings":          "# c\r\n.ssh/\r\n",
		"old Mac CR line endings":    ".ssh/\r*.pem\r",
		"trailing spaces":            ".ssh/   \n",
		"trailing tab":               ".ssh/\t\n",
		"leading spaces":             "   .ssh/\n",
		"no final newline":           ".ssh/",
		"non-breaking space":         ".ssh/ \n",
		"zero width space":           ".ssh/​\n",
		"inline comment":             ".ssh/   # my keys\n",
		"tilde user":                 "~me/.ssh/\n",
		"tilde alone slash":          "~/.ssh/\n",
		"fullwidth slash":            ".ssh／\n",
		"backslash separators":       ".ssh\\\n",
		"quoted":                     "\".ssh/\"\n",
		"double slash":               ".ssh//\n",
	} {
		loudOrWorks(t, name, text, target)
	}
}

// The errors say what is wrong, since a person has to fix the file.
func TestRuleFileMistakesAreReportedClearly(t *testing.T) {
	for text, want := range map[string]string{
		".ssh/  # my keys": "comment",
		"~me/.ssh/":        "~",
		"\".ssh/\"":        "quote",
		".ssh\u200b/":      "invisible",
		".ssh\uff0f":       "slash",
	} {
		_, err := Parse(strings.NewReader(text), ParseOptions{Home: "/Users/me"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want a message mentioning %q", text, err, want)
		}
	}
	// The things that look similar but are valid keep working.
	for _, ok := range []string{"\\#literal-hash-at-start", "a\\ #x", "name#with#hashes", "Caf\u00e9/", "**/Library/Application Support/Firefox/", "~/.ssh/"} {
		if _, err := Parse(strings.NewReader(ok), ParseOptions{Home: "/Users/me", AllowNegation: true}); err != nil {
			t.Errorf("%q must remain valid: %v", ok, err)
		}
	}
}
