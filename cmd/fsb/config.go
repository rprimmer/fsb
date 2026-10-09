package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rprimmer/fsb/internal/guard"
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

// checkBrowserName rejects names that `open` (or, on Linux, the program run)
// would take for one of its own options, or that could not be a real name.
func checkBrowserName(name string) error {
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("invalid --browser value %q: give an application name such as \"Google Chrome\" (macOS) or a program such as firefox (Linux)", name)
	}
	return nil
}

// openCommand returns the command that opens url in the default browser, or,
// if browser is set, in that browser: a macOS application, or on Linux a
// program run with the URL. It returns no command where fsb opens nothing,
// which includes Linux without a display (X11 or Wayland), as over ssh:
// xdg-open would then fall back to a text browser such as w3m, which fetches
// the single-use URL itself and leaves the printed one used up.
// The URL is always a single argument; nothing goes through a shell.
func openCommand(goos, browser, url string, display bool) (string, []string) {
	switch {
	case goos == "darwin" && browser == "":
		return "open", []string{url}
	case goos == "darwin":
		return "open", []string{"-a", browser, url}
	case goos == "linux" && browser == "" && display:
		return "xdg-open", []string{url}
	case goos == "linux" && browser == "":
		return "", nil
	case goos == "linux":
		return browser, []string{url}
	}
	return "", nil
}

// hasDisplay reports whether a graphical display is available (X11 or
// Wayland); macOS always has one.
func hasDisplay(getenv func(string) string) bool {
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

// openHint says what to do when the browser could not be opened.
func openHint(goos, browser string, err error) string {
	switch {
	case browser != "":
		return "open the URL above yourself, or check the --browser name"
	case goos == "linux" && errors.Is(err, exec.ErrNotFound):
		return "open the URL above yourself, or install xdg-utils, which provides xdg-open"
	}
	return "open the URL above yourself"
}

const lastPortFile = "lastport"

// readLastPort returns the port remembered from a previous successful start,
// or 0 if there is none or it cannot be used. A missing or malformed file is
// never an error: it just means there is nothing to remember yet.
func readLastPort(cfgDir string) int {
	data, err := os.ReadFile(filepath.Join(cfgDir, lastPortFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

// writeLastPort records port for next time, best effort: a failure here must
// never stop fsb from serving, so it is not reported to the caller.
func writeLastPort(cfgDir string, port int) {
	if os.MkdirAll(cfgDir, 0o700) != nil {
		return
	}
	_ = writeOwnFile(filepath.Join(cfgDir, lastPortFile), strconv.Itoa(port)+"\n")
}

// choosePort binds the loopback listener and decides which port it uses.
//
// If explicit is true (the user passed --port), port is used exactly as
// given (0 meaning "let the OS choose"), for this run only: it is not
// remembered, so a one-off choice, such as working around another program
// that happens to hold the usual port today, cannot silently become the
// standing default and drift the user away from it.
//
// Otherwise, the port remembered from a previous successful start (if any) is
// tried first, so that the browser's stored preferences, which are scoped to
// the page's full address including the port, survive a restart; the first
// time there is nothing remembered yet, a free port is chosen and then
// remembered. If a remembered port is now held by something else, this fails
// rather than silently falling back to a different port: a silent change
// would look like fsb had simply lost the user's preferences, with nothing
// to explain why.
func choosePort(cfgDir string, port int, explicit bool) (net.Listener, error) {
	if explicit {
		return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	remembered := readLastPort(cfgDir)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", remembered))
	if err != nil {
		if remembered != 0 && errors.Is(err, syscall.EADDRINUSE) {
			return nil, errors.New(portInUseMessage(remembered))
		}
		return nil, err
	}
	writeLastPort(cfgDir, ln.Addr().(*net.TCPAddr).Port)
	return ln, nil
}

// showRules prints the rules of one kind that fsb would apply, one per line,
// after a comment line saying where they come from: the user's file if it
// exists, otherwise the built-in defaults. For deny rules it also names any
// core rule the file leaves out, as fsb warns at startup.
func showRules(out io.Writer, kind, path string, set *rules.Set, coreMissing []string) error {
	src := path
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		src = "built-in defaults; " + path + " does not exist (fsb --init writes it)"
	} else if err != nil {
		return err
	}
	fmt.Fprintf(out, "# %s rules in effect (%s)\n", kind, src)
	for _, t := range set.Texts() {
		fmt.Fprintln(out, t)
	}
	if len(coreMissing) > 0 {
		fmt.Fprintf(out, "# warning: core deny rule(s) missing, so these paths are browsable: %s\n", strings.Join(coreMissing, ", "))
	}
	if kind == "deny" {
		fmt.Fprintln(out, "# also refused, though not a rule: any other name (hard link) of a denied file or of a file in a credential folder")
	}
	return nil
}

// newGuard creates the guard for roots. resolveRoots refuses "/" as spelled;
// this refuses it as resolved (a symbolic link to /, or a home that is one),
// by identity, unless allowSystemRoot.
func newGuard(roots []string, deny, hide *rules.Set, allowSystemRoot bool) (*guard.Guard, error) {
	g, err := guard.New(roots, deny, hide)
	if err != nil {
		return nil, err
	}
	if !allowSystemRoot && g.ServesSystemRoot() {
		return nil, errors.New("refusing to serve / without --allow-system-root (a root resolves to /)")
	}
	return g, nil
}
