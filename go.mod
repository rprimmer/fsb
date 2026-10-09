module github.com/rprimmer/fsb

go 1.26.0

// Builds use a Go release with the current standard-library security fixes.
toolchain go1.27.2

require (
	golang.org/x/sys v0.48.0
	golang.org/x/text v0.42.0
)
