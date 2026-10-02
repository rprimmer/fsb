// Package quicklook draws a picture of a document with macOS Quick Look, the
// same picture Finder shows, by running Apple's qlmanage tool.
package quicklook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Limits on one picture: how large qlmanage's output may be, and how long it
// may take. A real document takes well under a second, but qlmanage never
// finishes on one it cannot draw (a damaged file, say), so it is stopped.
const maxBytes = 20 << 20

var timeout = 5 * time.Second

// ErrUnavailable means no picture could be made: qlmanage is missing, failed,
// or had nothing to draw the document with.
var ErrUnavailable = errors.New("no Quick Look picture available")

// slots bounds how many qlmanage processes run at once.
var slots = make(chan struct{}, 2)

// Thumbnail returns a PNG picture of the document at path, at most size pixels
// on its longer side. path must be absolute and already checked by the guard;
// it is passed to qlmanage as one argument, never through a shell.
func Thumbnail(ctx context.Context, path string, size int) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("quicklook: path %q is not absolute", path)
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	dir, err := os.MkdirTemp("", "fsb-quicklook-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "qlmanage", "-t", "-s", fmt.Sprint(size), "-o", dir, path)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// qlmanage names the picture after the document; exactly one is expected.
	names, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil || len(names) != 1 {
		return nil, ErrUnavailable
	}
	fi, err := os.Stat(names[0])
	if err != nil || fi.Size() == 0 || fi.Size() > maxBytes {
		return nil, ErrUnavailable
	}
	data, err := os.ReadFile(names[0])
	if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, ErrUnavailable
	}
	return data, nil
}
