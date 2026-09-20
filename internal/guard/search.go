package guard

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// SearchMatch is one filename match.
type SearchMatch struct {
	Path    string    `json:"path"`
	Rel     string    `json:"rel"`
	Name    string    `json:"name"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// SearchLimits bounds a search.
type SearchLimits struct {
	MaxResults int
	MaxVisited int
}

// DefaultSearchLimits are the limits the server uses.
var DefaultSearchLimits = SearchLimits{MaxResults: 1000, MaxVisited: 500_000}

// Search finds entries under root whose name contains query (case-, accent-
// and normalization-insensitive), breadth-first so shallow matches come first.
//
// Every directory is opened through Open and every entry goes through the same
// filter as listings, so denied and hidden entries are never reported, denied
// or hidden directories are never entered, and symlinked directories are not
// followed (which also rules out cycles). It returns truncated=true if a limit
// stopped the search early. fn is called from the calling goroutine.
func (g *Guard) Search(ctx context.Context, root, query string, lim SearchLimits, fn func(SearchMatch) error) (visited int, truncated bool, err error) {
	q := fold(strings.TrimSpace(query))
	// Fail (as a listing would) if the root itself is not accessible. From here
	// on the search works on the root's real location, so a symlink to a
	// directory cannot change which entries are judged denied or hidden.
	f, fi, realRoot, err := g.open(canonPath(filepath.Clean(root)))
	if err != nil {
		return 0, false, err
	}
	f.Close()
	if !fi.IsDir() {
		return 0, false, ErrNotDir
	}
	root = realRoot
	if q == "" {
		return 0, false, nil
	}

	queue := []string{root}
	results := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return visited, truncated, err
		}
		dir := queue[0]
		queue = queue[1:]

		d, di, err := g.Open(dir)
		if err != nil || !di.IsDir() {
			continue // unreadable, vanished, or denied since listing: skip it
		}
		for {
			des, rerr := d.ReadDir(512)
			for _, de := range des {
				if visited >= lim.MaxVisited {
					d.Close()
					return visited, true, nil
				}
				e, ok := g.entry(dir, de)
				if !ok {
					continue
				}
				visited++
				child := filepath.Join(dir, e.Name)
				if strings.Contains(fold(e.Name), q) {
					rel := strings.TrimPrefix(child, root)
					rel = strings.TrimPrefix(rel, string(filepath.Separator))
					m := SearchMatch{Path: child, Rel: rel, Name: e.Name, IsDir: e.IsDir, Size: e.Size, ModTime: e.ModTime}
					if err := fn(m); err != nil {
						d.Close()
						return visited, truncated, err
					}
					results++
					if results >= lim.MaxResults {
						d.Close()
						return visited, true, nil
					}
				}
				if e.IsDir && !e.IsSymlink {
					queue = append(queue, child)
				}
			}
			if errors.Is(rerr, io.EOF) || rerr != nil {
				break
			}
			if err := ctx.Err(); err != nil {
				d.Close()
				return visited, truncated, err
			}
		}
		d.Close()
	}
	return visited, truncated, nil
}
