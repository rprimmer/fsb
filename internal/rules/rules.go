// Package rules implements gitignore-style path matching for fsb's two
// exclusion tiers (hide and deny).
//
// Matching is case-insensitive (full Unicode case folding) and normalized to
// mirror APFS; see Fold.
// A path matches if it, or any ancestor directory, is excluded; once an
// ancestor is excluded no later rule (including a "!" negation) can bring the
// path back, as in git.
package rules

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold reduces a path or pattern to the form in which two names are equal
// exactly when APFS (case-insensitive) treats them as the same name: Unicode
// normalization and *full* case folding, under which "ß" and "ss", "ſ" and
// "s", the Kelvin sign and "k", and ligatures such as "ﬁ" and "fi" all
// coincide. Rules are matched against folded text, so a denied name cannot be
// reached by spelling it with one of those characters.
func Fold(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	c := folders.Get().(cases.Caser)
	defer folders.Put(c)
	c.Reset()
	return norm.NFC.String(c.String(norm.NFC.String(s)))
}

// A Caser is stateful and not safe for concurrent use, and building one is not
// free; listings fold every name, so they are pooled.
var folders = sync.Pool{New: func() any { return cases.Fold() }}

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
	expr    string // the expression without flags, for merging

	// A rule whose pattern has no slash can only match the last path component,
	// so it is tested against that alone, anchored and short, instead of against
	// the whole path.
	base     bool
	baseRe   *regexp.Regexp
	baseExpr string
}

// Set is an ordered list of rules.
type Set struct {
	rules []rule

	// Combined expressions for a set without negation (see merge).
	fast                               bool
	baseAny, baseDir, pathAny, pathDir *regexp.Regexp
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
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// Editors add a byte order mark, and older ones end lines with a bare CR. A
	// rule file that loads but does nothing is the worst outcome, so both are
	// normalized here.
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	s := &Set{}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := checkLine(line); err != nil {
			return nil, fmt.Errorf("line %d (%q): %w", n+1, line, err)
		}
		rl, err := compile(line, o)
		if err != nil {
			return nil, fmt.Errorf("line %d (%q): %w", n+1, line, err)
		}
		s.rules = append(s.rules, rl)
	}
	s.merge()
	return s, nil
}

// slashLookalikes are characters that look like a path separator but are not.
const slashLookalikes = "\uff0f\uff3c\u2215\u2044\u2571\u29f8\u29f9"

// checkLine refuses text that a person may believe means something it does not:
// a rule that loads but silently never matches. (git shares these pitfalls; a
// deny file cannot afford them.)
func checkLine(l string) error {
	for i, r := range l {
		switch {
		case unicode.Is(unicode.Cf, r) || (unicode.IsSpace(r) && r != ' ' && r != '\t'):
			return fmt.Errorf("invisible character U+%04X; delete and retype the line", r)
		case strings.ContainsRune(slashLookalikes, r):
			return fmt.Errorf("U+%04X looks like a slash but is not one", r)
		case r == '#' && i > 0 && (l[i-1] == ' ' || l[i-1] == '\t') && (i < 2 || l[i-2] != '\\'): // an escaped space is part of the name
			return errors.New("inline comments are not supported: put the comment on its own line (write \\# for a literal #)")
		}
	}
	if len(l) >= 2 && (l[0] == '"' || l[0] == '\'') && l[len(l)-1] == l[0] {
		return errors.New("quotes are not part of the syntax: remove them")
	}
	if strings.HasPrefix(l, "!") && len(l) > 1 {
		l = l[1:]
	}
	if strings.HasPrefix(l, "~") && l != "~" && !strings.HasPrefix(l, "~/") {
		return errors.New("only ~ and ~/... are supported, not another user's home (~name)")
	}
	return nil
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
	comps := split(Fold(path))
	skip := len(split(Fold(from)))
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
	comp := path[strings.LastIndexByte(path, '/')+1:]
	if s.fast && !s.mayMatch(path, comp, isDir) {
		return Result{}
	}
	var res Result
	for _, r := range s.rules {
		if r.dirOnly && !isDir {
			continue
		}
		var hit bool
		if r.base {
			hit = r.baseRe.MatchString(comp)
		} else {
			hit = r.re.MatchString(path)
		}
		if hit {
			res = Result{Matched: !r.negate, Rule: r.text}
		}
	}
	return res
}

// mayMatch is a quick test, from the combined expressions, of whether any rule
// could match; false is final.
func (s *Set) mayMatch(path, comp string, isDir bool) bool {
	return (s.baseAny != nil && s.baseAny.MatchString(comp)) ||
		(isDir && s.baseDir != nil && s.baseDir.MatchString(comp)) ||
		(s.pathAny != nil && s.pathAny.MatchString(path)) ||
		(isDir && s.pathDir != nil && s.pathDir.MatchString(path))
}

// merge builds the combined expressions. It applies only to a set without
// negation, which is a plain union of rules (a deny set): most paths match
// nothing and there are many rules, so ruling them out in one pass is what keeps
// listing a large folder fast. The rules are still consulted one by one on a hit,
// to say which rule it was.
func (s *Set) merge() {
	var baseAny, baseDir, pathAny, pathDir []string
	for _, r := range s.rules {
		if r.negate {
			return
		}
		switch {
		case r.base && r.dirOnly:
			baseDir = append(baseDir, "(?:"+r.baseExpr+")")
		case r.base:
			baseAny = append(baseAny, "(?:"+r.baseExpr+")")
		case r.dirOnly:
			pathDir = append(pathDir, "(?:"+r.expr+")")
		default:
			pathAny = append(pathAny, "(?:"+r.expr+")")
		}
	}
	comp := func(parts []string) (*regexp.Regexp, bool) {
		if len(parts) == 0 {
			return nil, true
		}
		re, err := regexp.Compile("(?is)" + strings.Join(parts, "|"))
		return re, err == nil
	}
	var ok [4]bool
	s.baseAny, ok[0] = comp(baseAny)
	s.baseDir, ok[1] = comp(baseDir)
	s.pathAny, ok[2] = comp(pathAny)
	s.pathDir, ok[3] = comp(pathDir)
	s.fast = ok[0] && ok[1] && ok[2] && ok[3]
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

	glob, err := translate(Fold(p))
	if err != nil {
		return rl, err
	}
	// Trailing whitespace in a name is not significant to a rule: macOS allows
	// "id.pem " and ".env\t" as file names, and they must not slip past a rule
	// for "*.pem" or ".env". Flags: i = case-insensitive (APFS); s = "." also
	// matches a newline, which macOS also allows in a name, so that "**" cannot
	// be defeated by one.
	const tail = `\s*$`
	var expr string
	if anchored {
		expr = "^" + regexp.QuoteMeta(Fold(prefix)) + glob + tail
	} else {
		expr = "(?:^|/)" + glob + tail
	}
	re, err := regexp.Compile("(?is)" + expr)
	if err != nil {
		return rl, fmt.Errorf("invalid pattern: %w", err)
	}
	rl.re, rl.expr = re, expr
	if !anchored && !strings.Contains(p, "/") {
		rl.baseExpr = "^" + glob + tail
		if bre, err := regexp.Compile("(?is)" + rl.baseExpr); err == nil {
			rl.base, rl.baseRe = true, bre
		}
	}
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
			if strings.Contains(class, "/") {
				// A glob class never matches a slash, and an expression that
				// pretended otherwise could match across components.
				return "", errors.New("'/' is not allowed inside [...]")
			}
			if strings.HasPrefix(class, "!") || strings.HasPrefix(class, "^") {
				class = "^" + class[1:] + "/" // a negated class must still not match "/"
			}
			b.WriteString("[" + class + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String(), nil
}
