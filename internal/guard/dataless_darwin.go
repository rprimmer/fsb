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
	if datalessForTest != nil {
		return datalessForTest(fi)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Flags&sfDataless != 0
}

// refuseDataless is the one check that must run before anything reads the
// contents of fi: a file it never reads (Head, Meta's attributes, the sniffed
// endpoints), and, since a folder can be dataless too under the newer File
// Provider domains (iCloud Drive, OneDrive and similar), a directory before
// its entries are ever read. Reading either would risk triggering the
// provider to materialize it, which is exactly what fsb must never do
// implicitly.
func refuseDataless(fi fs.FileInfo) error {
	if isDataless(fi) {
		return ErrDataless
	}
	return nil
}
