package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rprimmer/fsb/internal/rules"
)

// initConfig writes the default rule files without ever overwriting one.
func initConfig(dir string, out io.Writer) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := []struct{ name, content string }{
		{"ignore", rules.IgnoreTemplate()},
		{"deny", rules.DenyTemplate()},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		err := writeNew(path, f.content)
		switch {
		case errors.Is(err, fs.ErrExist):
			fmt.Fprintf(out, "fsb: %s already exists; left unchanged\n", path)
		case err != nil:
			return err
		default:
			fmt.Fprintf(out, "fsb: wrote %s\n", path)
		}
	}
	return nil
}

func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// resolveRoots returns the roots to serve: the path argument (or home) plus any
// --root values. "/" needs an explicit opt-in.
func resolveRoots(home, arg string, extra []string, allowSystemRoot bool) ([]string, error) {
	first := home
	if arg != "" {
		first = arg
	}
	var roots []string
	for _, r := range append([]string{first}, extra...) {
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, err
		}
		abs = filepath.Clean(abs)
		if abs == string(filepath.Separator) && !allowSystemRoot {
			return nil, errors.New("refusing to serve / without --allow-system-root")
		}
		roots = append(roots, abs)
	}
	return roots, nil
}

// checkBrowserName rejects application names that `open` would take for one of
// its own options, or that could not be a real application name.
func checkBrowserName(name string) error {
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("invalid --browser value %q: give an application name such as \"Google Chrome\"", name)
	}
	return nil
}

// openCommand returns the command that opens url in the default browser, or,
// if browser is set, in that macOS application. The URL is always a single
// argument; nothing goes through a shell.
func openCommand(browser, url string) (string, []string) {
	if browser == "" {
		return "open", []string{url}
	}
	return "open", []string{"-a", browser, url}
}
