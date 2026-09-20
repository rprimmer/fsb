// Package rules implements gitignore-style path matching for fsb's two
// exclusion tiers (hide and deny).
//
// Matching is case-insensitive and Unicode-normalized (NFC) to mirror APFS.
// A path matches if it, or any ancestor directory, is excluded; once an
// ancestor is excluded no later rule (including a "!" negation) can bring the
// path back, as in git.
package rules

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ParseOptions controls how rule text is interpreted.
type ParseOptions struct {
	// Home expands a leading "~/" and is the default Base.
	Home string
	// Base anchors patterns that contain a slash but do not start with "/" or
	// "~/". Defaults to Home.
	Base string
	// AllowNegation permits "!pattern" lines. Deny files must leave this false.
	AllowNegation bool
}

type rule struct {
	text    string
	negate  bool
	dirOnly bool
	re      *regexp.Regexp
}

// Set is an ordered list of rules.
type Set struct {
	rules []rule
}

// Result reports whether a path is excluded and by which rule.
type Result struct {
	Matched bool
	Rule    string
}

// Parse reads rules, one per line. Blank lines and lines starting with "#"
// are skipped. Any malformed line is an error so callers can fail closed.
func Parse(r io.Reader, o ParseOptions) (*Set, error) {
	if o.Base == "" {
		o.Base = o.Home
	}
	s := &Set{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		rl, err := compile(text, o)
		if err != nil {
			return nil, fmt.Errorf("line %d (%q): %w", n, text, err)
		}
		s.rules = append(s.rules, rl)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// Texts returns the source text of every rule, in order.
func (s *Set) Texts() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.rules))
	for i, r := range s.rules {
		out[i] = r.text
	}
	return out
}

// Match reports whether the absolute, cleaned path is excluded. isDir says
// whether the final component is a directory (for "dir/" patterns).
func (s *Set) Match(path string, isDir bool) Result {
	return s.MatchBelow("/", path, isDir)
}

// MatchBelow is like Match but ignores ancestors at or above from. It is used
// for hide rules when listing a directory: entering a hidden directory on
// purpose should show its contents.
func (s *Set) MatchBelow(from, path string, isDir bool) Result {
	if s == nil || len(s.rules) == 0 {
		return Result{}
	}
	comps := split(norm.NFC.String(path))
	skip := len(split(norm.NFC.String(from)))
	for i := skip; i < len(comps); i++ {
		prefix := "/" + strings.Join(comps[:i+1], "/")
		prefixIsDir := i < len(comps)-1 || isDir
		if r := s.eval(prefix, prefixIsDir); r.Matched {
			return r
		}
	}
	return Result{}
}

// eval applies every rule to one path; the last matching rule wins.
func (s *Set) eval(path string, isDir bool) Result {
	var res Result
	for _, r := range s.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(path) {
			res = Result{Matched: !r.negate, Rule: r.text}
		}
	}
	return res
}

func split(path string) []string {
	var out []string
	for _, c := range strings.Split(path, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

func compile(text string, o ParseOptions) (rule, error) {
	rl := rule{text: text}
	p := text

	if strings.HasPrefix(p, "!") {
		if !o.AllowNegation {
			return rl, errors.New("negation (!) is not allowed in this file")
		}
		rl.negate = true
		p = p[1:]
	}
	if strings.HasSuffix(p, "/") {
		rl.dirOnly = true
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		return rl, errors.New("empty pattern")
	}

	// prefix is literal text (regexp-quoted), p is the glob part.
	var prefix string
	anchored := false
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		if o.Home == "" {
			return rl, errors.New("'~' used but no home directory is known")
		}
		prefix, p, anchored = strings.TrimRight(o.Home, "/"), p[1:], true
	case strings.HasPrefix(p, "**/"):
		p = strings.TrimLeft(strings.TrimPrefix(p, "**/"), "/")
		if p == "" {
			return rl, errors.New("empty pattern")
		}
	case strings.HasPrefix(p, "/"):
		anchored = true
	case strings.Contains(p, "/"):
		if o.Base == "" {
			return rl, errors.New("relative pattern used but no base directory is known")
		}
		prefix, p, anchored = strings.TrimRight(o.Base, "/"), "/"+p, true
	}

	glob, err := translate(norm.NFC.String(p))
	if err != nil {
		return rl, err
	}
	var expr string
	if anchored {
		expr = "^" + regexp.QuoteMeta(norm.NFC.String(prefix)) + glob + "$"
	} else {
		expr = "(?:^|/)" + glob + "$"
	}
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return rl, fmt.Errorf("invalid pattern: %w", err)
	}
	rl.re = re
	return rl, nil
}

// translate converts a gitignore glob to a regular expression fragment.
func translate(p string) (string, error) {
	var b strings.Builder
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch c {
		case '\\':
			if i+1 >= len(rs) {
				return "", errors.New("trailing backslash")
			}
			i++
			b.WriteString(regexp.QuoteMeta(string(rs[i])))
		case '*':
			j := i
			for j < len(rs) && rs[j] == '*' {
				j++
			}
			atSegmentStart := i == 0 || rs[i-1] == '/'
			switch {
			case j-i >= 2 && atSegmentStart && j < len(rs) && rs[j] == '/':
				b.WriteString(`(?:.*/)?`) // "**/": zero or more directories
				i = j
			case j-i >= 2 && atSegmentStart && j == len(rs):
				b.WriteString(`.*`) // trailing "/**": everything inside
				i = j - 1
			default:
				b.WriteString(`[^/]*`)
				i = j - 1
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			j := i + 1
			if j < len(rs) && (rs[j] == '!' || rs[j] == '^') {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(`\[`)
				continue
			}
			class := string(rs[i+1 : j])
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String(), nil
}
