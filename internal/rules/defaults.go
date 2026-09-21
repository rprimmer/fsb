package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// CoreDeny lists locations and files that hold credentials or secrets. They are
// active by default and compiled in, so they apply even if no deny file exists.
// The list is versioned with the release; additions are called out in release
// notes.
//
// The last four are patterns that match at any depth. They are deliberately
// broad: ".env"/".env.*" also match a directory named .env (such as a Python
// virtualenv) and committed templates like .env.example, and "*.key" is also
// the extension of Keynote presentations. Anyone who needs those can edit their
// deny file; fsb then warns that a core rule is off (see LoadDeny).
var CoreDeny = []string{
	".ssh/",
	".aws/",
	".gnupg/",
	"**/.config/gh/",
	".netrc",
	".kube/",
	"**/Library/Keychains/",
	"**/Library/Application Support/Google/Chrome/",
	"**/Library/Application Support/Firefox/",
	"**/Library/Safari/",
	"**/Library/Cookies/",
	".env",
	".env.*",
	"*.pem",
	"*.key",
}

// OptionalDeny lists situational rules shipped commented out.
var OptionalDeny = []string{
	"~/.docker/config.json",
	"~/.npmrc",
	"~/Library/Mail/",
	"~/Library/Messages/",
}

// DefaultIgnore is the built-in hide list, used when no ignore file exists.
var DefaultIgnore = []string{
	".DS_Store",
	".git/",
	"node_modules/",
}

// DenyTemplate returns the contents `fsb --init` writes to the deny file.
func DenyTemplate() string {
	var b strings.Builder
	b.WriteString("# fsb deny rules: paths matching these are never served (HTTP 404).\n")
	b.WriteString("# Global config only; gitignore syntax; negation (!) is not allowed.\n")
	b.WriteString("# Deny always wins over ignore.\n\n")
	b.WriteString("# --- Core (active by default: known credential/secret locations and files) ---\n")
	b.WriteString("# Removing a core rule makes fsb warn at startup and in the UI.\n")
	for _, r := range CoreDeny {
		b.WriteString(r + "\n")
	}
	b.WriteString("\n# --- Optional (commented out: uncomment to enable) ---\n")
	for _, r := range OptionalDeny {
		b.WriteString("# " + r + "\n")
	}
	return b.String()
}

// IgnoreTemplate returns the contents `fsb --init` writes to the ignore file.
func IgnoreTemplate() string {
	var b strings.Builder
	b.WriteString("# fsb ignore rules: matching entries are hidden from listings and search.\n")
	b.WriteString("# They stay reachable by direct path; use the deny file to block access.\n")
	b.WriteString("# gitignore syntax, including ! negation.\n\n")
	for _, r := range DefaultIgnore {
		b.WriteString(r + "\n")
	}
	return b.String()
}

// CoreMissing returns core rules that are not present in s.
func CoreMissing(s *Set) []string {
	// Compared as names are compared (Fold), so "~/.SSH" is recognised as the
	// core rule "~/.ssh". Equal text always denies the same things, so this never
	// reports a rule as present that is not ("~/.aws/" is a different rule from
	// "~/.aws": it applies to directories only).
	norm := Fold
	have := map[string]bool{}
	for _, t := range s.Texts() {
		have[norm(t)] = true
	}
	var missing []string
	for _, c := range CoreDeny {
		if !have[norm(c)] {
			missing = append(missing, c)
		}
	}
	return missing
}

// LoadDeny loads the deny file. If the file does not exist the built-in core
// rules apply. If it exists it is authoritative, and coreMissing lists any core
// rules the user removed or commented out (the caller must warn about them).
// A malformed file is an error: the caller must refuse to start.
func LoadDeny(path, home string) (set *Set, coreMissing []string, err error) {
	opts := ParseOptions{Home: home}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s, perr := Parse(strings.NewReader(strings.Join(CoreDeny, "\n")), opts)
		return s, nil, perr
	}
	if err != nil {
		return nil, nil, err
	}
	s, err := Parse(strings.NewReader(string(data)), opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, CoreMissing(s), nil
}

// LoadIgnore loads the global ignore file, falling back to DefaultIgnore.
func LoadIgnore(path, home string) (*Set, error) {
	opts := ParseOptions{Home: home, AllowNegation: true}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Parse(strings.NewReader(strings.Join(DefaultIgnore, "\n")), opts)
	}
	if err != nil {
		return nil, err
	}
	s, err := Parse(strings.NewReader(string(data)), opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}
