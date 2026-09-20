package guard

import (
	"io/fs"
	"syscall"
	"testing"
	"time"
)

type fakeInfo struct{ sys any }

func (fakeInfo) Name() string       { return "f" }
func (fakeInfo) Size() int64        { return 1 }
func (fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any         { return f.sys }

func TestIsDataless(t *testing.T) {
	if !isDataless(fakeInfo{&syscall.Stat_t{Flags: sfDataless}}) {
		t.Error("SF_DATALESS must be detected")
	}
	if !isDataless(fakeInfo{&syscall.Stat_t{Flags: sfDataless | 0x20}}) {
		t.Error("SF_DATALESS must be detected among other flags")
	}
	if isDataless(fakeInfo{&syscall.Stat_t{Flags: 0x20}}) || isDataless(fakeInfo{&syscall.Stat_t{}}) {
		t.Error("ordinary files are not dataless")
	}
	if isDataless(fakeInfo{nil}) {
		t.Error("unknown Sys() must not be treated as dataless")
	}
}
