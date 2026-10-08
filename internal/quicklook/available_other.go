//go:build !darwin

package quicklook

// available says whether this system has Quick Look; only macOS does, so
// elsewhere no picture is attempted and no program named qlmanage is run.
const available = false
