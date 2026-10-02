package guard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The scan budget bounds the work, so entries that are examined and then
// rejected (denied or hidden) count against it like any other. The count
// reported back is still only of visible entries, so it reveals nothing about
// how many were excluded.
func TestSearchBudgetCountsExcludedEntries(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
	}{
		{"all denied", []string{"k0.pem", "k1.pem", "k2.pem", "k3.pem", "k4.pem", "k5.pem"}},
		{"all hidden", []string{".DS_Store", "node_modules/", ".git/"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t)
			dir := filepath.Join(fx.home, "budget")
			for _, f := range c.files {
				if f[len(f)-1] == '/' {
					write(t, filepath.Join(dir, f, "x"), "x")
				} else {
					write(t, filepath.Join(dir, f), "x")
				}
			}
			visited, truncated, err := fx.g.Search(context.Background(), dir, "zzz", SearchOptions{MaxResults: 100, MaxVisited: 1}, func(SearchMatch) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if !truncated {
				t.Errorf("examined %d excluded entries with a budget of 1 and did not stop", len(c.files))
			}
			if visited != 0 {
				t.Errorf("visited = %d; it must count visible entries only", visited)
			}
		})
	}
}

// A folder that fails partway through being read (an I/O error, a network
// volume going away) leaves the search incomplete, and the summary says so,
// rather than the results looking complete.
func TestSearchReportsFoldersThatCouldNotBeReadCompletely(t *testing.T) {
	fx := newFixture(t)
	dir := filepath.Join(fx.home, "partial")
	for _, n := range []string{"a1", "a2", "a3"} {
		write(t, filepath.Join(dir, "sub", n), "x")
	}
	searchReadErrForTest = func(d string) error {
		if filepath.Base(d) == "sub" {
			return errors.New("injected I/O error")
		}
		return nil
	}
	t.Cleanup(func() { searchReadErrForTest = nil })
	sum, err := fx.g.SearchSummary(context.Background(), dir, "a", DefaultSearchLimits, func(SearchMatch) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if sum.Unreadable != 1 {
		t.Errorf("summary %+v: want one folder that could not be read completely", sum)
	}
	searchReadErrForTest = nil
	if sum, _ := fx.g.SearchSummary(context.Background(), dir, "a", DefaultSearchLimits, func(SearchMatch) error { return nil }); sum.Unreadable != 0 {
		t.Errorf("summary %+v without errors", sum)
	}
}
