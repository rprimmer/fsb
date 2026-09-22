//go:build !darwin

package guard

import "io/fs"

// isDataless is always false where the platform has no dataless-file flag.
func isDataless(fs.FileInfo) bool { return false }

// refuseDataless mirrors dataless_darwin.go's; nothing is ever dataless here.
func refuseDataless(fs.FileInfo) error { return nil }
