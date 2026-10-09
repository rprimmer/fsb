//go:build !darwin && !linux

package main

// openBrowser is never called here: openCommand returns no command.
func openBrowser(name string, args []string) error { return nil }
