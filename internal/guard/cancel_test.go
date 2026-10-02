package guard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Expensive work stops when the request is abandoned or its time is up, rather
// than running to its size limits on slow storage.

// slowReader yields a byte at a time, slowly and without end.
type slowReader struct{}

func (slowReader) Read(p []byte) (int, error) {
	time.Sleep(time.Millisecond)
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func TestCopyStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	budget := int64(MaxQuickLookBytes)
	done := make(chan error, 1)
	go func() { done <- copyBounded(ctx, slowReader{}, filepath.Join(t.TempDir(), "out"), &budget) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want the deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the copy did not stop when its context ended")
	}
}

func TestQuickLookCopyHonorsACanceledContext(t *testing.T) {
	fx := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc := filepath.Join(fx.home, "proj", "Report.docx")
	write(t, doc, "PK document")
	if _, err := fx.g.CopyForQuickLookContext(ctx, doc, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("file: err = %v, want canceled", err)
	}
	pkg := filepath.Join(fx.home, "proj", "Deck.pages")
	write(t, filepath.Join(pkg, "Index.zip"), "x")
	write(t, filepath.Join(pkg, "Data", "a.png"), "x")
	if _, err := fx.g.CopyForQuickLookContext(ctx, pkg, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("package: err = %v, want canceled", err)
	}
}

func TestArchiveListingHonorsItsContext(t *testing.T) {
	fx := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, data := range map[string][]byte{
		"a.zip":    makeZip(t, "a", "b"),
		"a.tar.gz": gz(makeTar(t, "a", "b")),
		"a.tar":    makeTar(t, "a", "b"),
	} {
		p := filepath.Join(fx.home, "arch", name)
		put(t, p, data)
		if _, err := fx.g.ListArchiveContext(ctx, p); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want canceled", name, err)
		}
	}
}

// Canceled partway through a long tar.gz: it stops there.
func TestTarListingStopsMidStream(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for i := range 5000 {
		tw.WriteHeader(&tar.Header{Name: itoa(i), Size: 0, Typeflag: tar.TypeReg, Mode: 0o644})
	}
	tw.Close()
	gw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r := &cancelAfter{r: bytes.NewReader(buf.Bytes()), n: 2048, cancel: cancel}
	zr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listTar(ctx, zr, "tar.gz"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want canceled", err)
	}
}

// cancelAfter cancels its context once n bytes have been read through it.
type cancelAfter struct {
	r      *bytes.Reader
	n      int
	cancel func()
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	if c.n -= k; c.n <= 0 {
		c.cancel()
	}
	return k, err
}
