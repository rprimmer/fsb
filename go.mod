module github.com/rprimmer/fsb

// The minimum is the Go release with the current standard-library security
// fixes. `go install` ignores a toolchain line, so only this line makes an
// older Go switch to it.
go 1.27.2

require (
	golang.org/x/sys v0.48.0
	golang.org/x/text v0.42.0
)
