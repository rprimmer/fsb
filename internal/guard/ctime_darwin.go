package guard

import "syscall"

// ctimeOf is a file's status-change time in nanoseconds.
func ctimeOf(st *syscall.Stat_t) int64 { return st.Ctimespec.Nano() }
