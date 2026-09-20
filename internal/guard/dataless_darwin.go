package guard

import (
	"io/fs"
	"syscall"
)

// sfDataless is SF_DATALESS from <sys/stat.h>: the file's contents live in a
// cloud provider (iCloud Drive and other File Provider domains) and reading
// them would trigger a download.
const sfDataless = 0x40000000

func isDataless(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Flags&sfDataless != 0
}
