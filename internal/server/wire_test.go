package server

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// Linux allows any bytes but "/" and NUL in a name, so a name need not be
// UTF-8, and JSON would replace each stray byte with U+FFFD: the browser could
// show such a file but never ask for it again. wireName keeps every byte.
func TestWireName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"plain.txt", "plain.txt"},
		{"café.txt", "café.txt"},         // valid UTF-8 is unchanged
		{"caf\xe9.txt", "caf\x00E9.txt"}, // Latin-1 é
		{"/home/u/\xff\xfe/x", "/home/u/\x00FF\x00FE/x"},
		{"\xe2\x82", "\x00E2\x0082"},           // a cut-off three-byte character
		{"ok\xc3\xa9\x80", "ok\xc3\xa9\x0080"}, // valid é, then a stray byte
	} {
		got := wireName(c.in)
		if got != c.want {
			t.Errorf("wireName(%q) = %q, want %q", c.in, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("wireName(%q) = %q is not valid UTF-8", c.in, got)
		}
		b, _ := json.Marshal(got)
		var back string
		if json.Unmarshal(b, &back); back != c.want {
			t.Errorf("wireName(%q) does not survive JSON: %q", c.in, back)
		}
	}
}

// Names are never escaped into each other: a NUL can't occur in a real name,
// so an escape can't be mistaken for one, and different bytes stay different.
func TestWireNameIsOneToOne(t *testing.T) {
	seen := map[string]string{}
	for b := 0; b < 256; b++ {
		for _, in := range []string{string([]byte{byte(b)}), "a" + string([]byte{byte(b)}) + "b"} {
			out := wireName(in)
			if prev, ok := seen[out]; ok && prev != in {
				t.Fatalf("wireName(%q) = wireName(%q) = %q", in, prev, out)
			}
			seen[out] = in
		}
	}
}
