//go:build !darwin

package guard

import "io/fs"

// isDataless is always false where the platform has no dataless-file flag.
func isDataless(fs.FileInfo) bool { return false }
