package guard

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
	"time"
)

type fakeInfo struct {
	sys   any
	isDir bool
}

func (fakeInfo) Name() string       { return "f" }
func (fakeInfo) Size() int64        { return 1 }
func (fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool      { return f.isDir }
func (f fakeInfo) Sys() any         { return f.sys }

func TestIsDataless(t *testing.T) {
	if !isDataless(fakeInfo{sys: &syscall.Stat_t{Flags: sfDataless}}) {
		t.Error("SF_DATALESS must be detected")
	}
	if !isDataless(fakeInfo{sys: &syscall.Stat_t{Flags: sfDataless | 0x20}}) {
		t.Error("SF_DATALESS must be detected among other flags")
	}
	if isDataless(fakeInfo{sys: &syscall.Stat_t{Flags: 0x20}}) || isDataless(fakeInfo{sys: &syscall.Stat_t{}}) {
		t.Error("ordinary files are not dataless")
	}
	if isDataless(fakeInfo{sys: nil}) {
		t.Error("unknown Sys() must not be treated as dataless")
	}
}

// A real bug, found from a screenshot of the running app: listing a folder
// that is itself dataless (a File Provider domain such as OneDrive or iCloud
// Drive can make a whole folder, not only files inside it, a placeholder)
// went straight into ReadDir, which returned an errno mapErr did not
// recognize, so it surfaced as an opaque 500 instead of the "stored in the
// cloud" answer every other cloud-only path gets. ListFunc and Search now
// check refuseDataless before reading, the same way Open's fstat check
// already refuses a socket or a FIFO.
//
// The real File Provider flag cannot be set from an unprivileged test (SF_
// flags of this kind are not settable by chflags outside the provider
// itself, confirmed empirically: it reports success but does not stick), so
// this tests the logic both call sites now share, the same way TestIsDataless
// tests isDataless itself, rather than the real syscall path end to end.
func TestRefuseDataless(t *testing.T) {
	if err := refuseDataless(fakeInfo{sys: &syscall.Stat_t{Flags: sfDataless}}); !errors.Is(err, ErrDataless) {
		t.Errorf("a dataless file: err = %v, want ErrDataless", err)
	}
	if err := refuseDataless(fakeInfo{sys: &syscall.Stat_t{Flags: sfDataless}, isDir: true}); !errors.Is(err, ErrDataless) {
		t.Errorf("a dataless directory: err = %v, want ErrDataless (folders can be dataless too)", err)
	}
	if err := refuseDataless(fakeInfo{sys: &syscall.Stat_t{}}); err != nil {
		t.Errorf("an ordinary file: err = %v, want nil", err)
	}
}
