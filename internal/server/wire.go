package server

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rprimmer/fsb/internal/guard"
)

// wireName makes a name or path safe to send as JSON without losing bytes.
// Linux allows any bytes in a name but "/" and NUL, so a name need not be
// UTF-8, and JSON would replace each stray byte with U+FFFD: the browser could
// show the file but never ask for it again. Each byte that is not part of
// valid UTF-8 is sent as NUL followed by its value in two uppercase hex
// digits ("caf\xe9" becomes "caf\x00E9"). A NUL cannot occur in a real name,
// so the escape is never ambiguous. The browser shows it as a marker and turns
// it back into the byte (%E9) when it sends the path, so requests carry the
// real bytes and nothing on the server decodes it. Valid UTF-8 is unchanged.
func wireName(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			fmt.Fprintf(&b, "\x00%02X", s[i])
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func wireNames(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = wireName(s)
	}
	return out
}

// The functions below return copies with every name and path passed through
// wireName; the guard's own values are not changed.

func wireEntries(es []guard.Entry) []guard.Entry {
	out := make([]guard.Entry, len(es))
	for i, e := range es {
		e.Name = wireName(e.Name)
		out[i] = e
	}
	return out
}

func wireMatches(ms []guard.SearchMatch) []guard.SearchMatch {
	out := make([]guard.SearchMatch, len(ms))
	for i, m := range ms {
		m.Path, m.Rel, m.Name = wireName(m.Path), wireName(m.Rel), wireName(m.Name)
		out[i] = m
	}
	return out
}

func wireMeta(m guard.Meta) guard.Meta {
	c := m
	c.Name, c.Path, c.SymlinkTarget = wireName(m.Name), wireName(m.Path), wireName(m.SymlinkTarget)
	if m.XAttrs != nil { // nil and empty encode differently
		c.XAttrs = make([]guard.XAttr, len(m.XAttrs))
		for i, x := range m.XAttrs {
			x.Name = wireName(x.Name)
			c.XAttrs[i] = x
		}
	}
	return c
}

func wireArchive(l guard.ArchiveListing) guard.ArchiveListing {
	c := l
	if l.Entries != nil {
		c.Entries = make([]guard.ArchiveEntry, len(l.Entries))
		for i, e := range l.Entries {
			e.Name = wireName(e.Name)
			c.Entries[i] = e
		}
	}
	return c
}
