//go:build !darwin && !linux

package main

import "context"

// openBrowser is never called here: openCommand returns no command.
func openBrowser(context.Context, string, []string) error { return nil }
