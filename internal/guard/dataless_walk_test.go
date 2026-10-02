package guard

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Real dataless folders (cloud-only, in a File Provider domain) cannot be made
// in a test, so these mark folders as dataless by name through the test hook
// in isDataless. What they check is the call sites: that no walk enumerates
// such a folder.

const cloudDir = "cloud-only-folder"

func markCloudDirsDataless(t *testing.T) {
	t.Helper()
	datalessForTest = func(fi fs.FileInfo) bool { return fi.IsDir() && fi.Name() == cloudDir }
	t.Cleanup(func() { datalessForTest = nil })
}

func TestLinkIndexDoesNotEnterDatalessFolders(t *testing.T) {
	markCloudDirsDataless(t)
	fx := newFixture(t)
	fx.g.linkRebuildMin, fx.g.linkSync = 0, true
	inside := filepath.Join(fx.home, "proj", cloudDir, "deeper")
	write(t, filepath.Join(inside, "a.txt"), "x")
	link(t, filepath.Join(inside, "a.txt"), filepath.Join(inside, "b.txt"))
	write(t, filepath.Join(fx.home, "proj", "c.txt"), "x")
	link(t, filepath.Join(fx.home, "proj", "c.txt"), filepath.Join(fx.home, "proj", "d.txt"))
	fx.g.StartLinkIndex()
	fx.g.links.mu.Lock()
	defer fx.g.links.mu.Unlock()
	if !fx.g.links.ready {
		t.Fatal("index not built")
	}
	sawOrdinary := false
	for _, e := range fx.g.links.byID {
		for _, n := range e.names {
			if strings.Contains(n, cloudDir) {
				t.Errorf("the walk entered a dataless folder: %s", n)
			}
			sawOrdinary = sawOrdinary || strings.HasSuffix(n, "c.txt")
		}
	}
	if !sawOrdinary {
		t.Error("the walk must still index the rest of the tree")
	}
}

func TestQuickLookRefusesAPackageWithADatalessFolder(t *testing.T) {
	markCloudDirsDataless(t)
	fx := newFixture(t)
	pkg := filepath.Join(fx.home, "proj", "Report.pages")
	write(t, filepath.Join(pkg, "Index.zip"), "x")
	write(t, filepath.Join(pkg, cloudDir, "Data", "image.png"), "x")
	if _, err := fx.g.CopyForQuickLook(pkg, t.TempDir()); !errors.Is(err, ErrDataless) {
		t.Errorf("err = %v, want ErrDataless for a package with a cloud-only folder", err)
	}
	ok := filepath.Join(fx.home, "proj", "Plain.pages")
	write(t, filepath.Join(ok, "Index.zip"), "x")
	write(t, filepath.Join(ok, "Data", "image.png"), "x")
	if _, err := fx.g.CopyForQuickLook(ok, t.TempDir()); err != nil {
		t.Errorf("an ordinary package: %v", err)
	}
}
