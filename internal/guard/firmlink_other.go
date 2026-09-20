//go:build !darwin

package guard

// canonPath is the identity where the platform has no firmlinks.
func canonPath(p string) string { return p }
