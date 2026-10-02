//go:build !darwin

package guard

import "io/fs"

// isDataless is always false where the platform has no dataless-file flag
// (except as a test decides).
func isDataless(fi fs.FileInfo) bool {
	return datalessForTest != nil && datalessForTest(fi)
}

// refuseDataless mirrors dataless_darwin.go's.
func refuseDataless(fi fs.FileInfo) error {
	if isDataless(fi) {
		return ErrDataless
	}
	return nil
}
