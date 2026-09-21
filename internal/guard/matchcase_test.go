package guard

import (
	"path/filepath"
	"testing"
)

func TestSearchMatchCase(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, "proj", "Report-final.txt"), []byte("x"))
	put(t, filepath.Join(fx.home, "proj", "report-draft.txt"), []byte("x"))
	put(t, filepath.Join(fx.home, "proj", "REPORT-ALL.txt"), []byte("x"))

	ms, _, _, err := searchAll(t, fx.g, fx.home, "Report", DefaultSearchLimits)
	if err != nil || len(ms) != 3 {
		t.Fatalf("case-insensitive: %v %v, want all three", rels(ms), err)
	}
	opts := DefaultSearchLimits
	opts.MatchCase = true
	ms, _, _, err = searchAll(t, fx.g, fx.home, "Report", opts)
	if err != nil || len(ms) != 1 || ms[0].Name != "Report-final.txt" {
		t.Fatalf("match case: %v %v, want only Report-final.txt", rels(ms), err)
	}
	ms, _, _, _ = searchAll(t, fx.g, fx.home, "REPORT", opts)
	if len(ms) != 1 || ms[0].Name != "REPORT-ALL.txt" {
		t.Fatalf("match case upper: %v", rels(ms))
	}
}

// macOS may store "é" as e + combining accent; a user types the single
// character. Both must match, with or without case sensitivity.
func TestSearchMatchCaseStillNormalizesUnicode(t *testing.T) {
	fx := newFixture(t)
	put(t, filepath.Join(fx.home, "proj", "Cafe\u0301-Menu.txt"), []byte("x")) // decomposed on disk
	for _, matchCase := range []bool{false, true} {
		opts := DefaultSearchLimits
		opts.MatchCase = matchCase
		ms, _, _, err := searchAll(t, fx.g, fx.home, "Caf\u00e9", opts) // composed query
		if err != nil || len(ms) != 1 {
			t.Errorf("matchCase=%v: found %v (%v), want the accented file", matchCase, rels(ms), err)
		}
	}
	opts := DefaultSearchLimits
	opts.MatchCase = true
	if ms, _, _, _ := searchAll(t, fx.g, fx.home, "cafe", opts); len(ms) != 0 {
		t.Errorf("match case must still tell c from C: %v", rels(ms))
	}
}
