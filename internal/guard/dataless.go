package guard

import "io/fs"

// datalessForTest, when set, decides isDataless instead of the file's flags,
// so tests can stand in for cloud-only folders, which cannot be made locally.
var datalessForTest func(fs.FileInfo) bool
