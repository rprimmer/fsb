// Command fsb serves a read-only, local-only web view of your filesystem.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/rules"
	"github.com/rprimmer/fsb/internal/server"
)

const usage = `usage: fsb [flags] [path]

Serves a read-only web view of your filesystem on 127.0.0.1 only.
With no path, the root is your home directory.

flags:
`

// singleDashFlag matches the leading "  -name" that flag.PrintDefaults always
// prints for each flag, so it can be rewritten to "  --name": fsb accepts one
// or two leading hyphens equally, but the manual page writes them all with
// two, and showing one here needlessly makes "fsb -h" look like it disagrees.
var singleDashFlag = regexp.MustCompile(`(?m)^  -`)

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	if isBackgroundChild() {
		os.Exit(childMain(os.Args[1:]))
	}
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.As(err, new(errAlreadyReported)) {
			fmt.Fprintln(os.Stderr, "fsb:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("fsb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		var buf bytes.Buffer
		fs.SetOutput(&buf)
		fs.PrintDefaults()
		fs.SetOutput(stderr)
		fmt.Fprint(stderr, singleDashFlag.ReplaceAllString(buf.String(), "  --"))
	}
	var (
		extraRoots stringList
		debug      = fs.Bool("debug", false, "verbose logging; denied paths answer 404 naming the matching rule")
		noOpen     = fs.Bool("no-open", false, "do not open the browser; just print the URL")
		background = fs.Bool("background", false, "run in the background: print the URL and return to the shell (stop it with --stop)")
		stopFlag   = fs.Bool("stop", false, "stop the running fsb (started with --background, or holding the usual port), and exit")
		status     = fs.Bool("status", false, "say whether fsb is running, with its process, port and start time, and exit")
		browser    = fs.String("browser", "", `browser to open the URL in: a macOS application such as "Google Chrome", or on Linux a program such as firefox (default: your default browser)`)
		doInit     = fs.Bool("init", false, "write the default ignore and deny files to ~/.config/fsb and exit")
		showDeny   = fs.Bool("show-deny", false, "print the deny rules in effect, and where they come from, and exit")
		showIgnore = fs.Bool("show-ignore", false, "print the ignore (hide) rules in effect, and where they come from, and exit")
		version    = fs.Bool("version", false, "print the version and the commit fsb was built from, and exit")
		sysRoot    = fs.Bool("allow-system-root", false, "allow / as a root")
		portFlag   = fs.Int("port", 0, "use exactly this port for this run only (0 lets the OS choose); without this flag, fsb reuses the port it last used successfully, so the browser's stored preferences (column widths, the preview pane's width) survive a restart, and falls back to a free port the first time there is nothing to reuse yet")
	)
	fs.Var(&extraRoots, "root", "additional root directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *version {
		fmt.Fprintln(stdout, buildVersion())
		return nil
	}
	if fs.NArg() > 1 {
		return errors.New("at most one path argument is allowed")
	}
	if err := checkBrowserName(*browser); err != nil {
		return err
	}
	if *portFlag < 0 || *portFlag > 65535 {
		return fmt.Errorf("invalid --port value %d: must be 0-65535", *portFlag)
	}
	portExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portExplicit = true
		}
	})

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Rules such as ~/.ssh/ are compared with real paths, so build them from the
	// real home directory even if $HOME is a symlink or an alias.
	home = guard.RealPath(home)
	cfgDir := filepath.Join(home, ".config", "fsb")

	if *doInit {
		return initConfig(cfgDir, stdout)
	}
	if *stopFlag {
		return stopRunning(cfgDir, stdout)
	}
	if *status {
		showStatus(cfgDir, stdout)
		return nil
	}
	// The background child knows itself by its handoff, not by childEnv, which
	// it removes so that nothing it starts inherits it.
	if _, isChild := stdout.(*handoff); *background && !isChild {
		return startBackground(cfgDir, args, stdout)
	}

	// A malformed rules file is fatal: never start with weaker rules than the
	// user wrote.
	denyPath, ignorePath := filepath.Join(cfgDir, "deny"), filepath.Join(cfgDir, "ignore")
	deny, coreMissing, err := rules.LoadDeny(denyPath, home)
	if err != nil {
		return fmt.Errorf("deny rules: %w", err)
	}
	hide, err := rules.LoadIgnore(ignorePath, home)
	if err != nil {
		return fmt.Errorf("ignore rules: %w", err)
	}
	if *showDeny || *showIgnore {
		if *showDeny {
			if err := showRules(stdout, "deny", denyPath, deny, coreMissing); err != nil {
				return err
			}
		}
		if *showIgnore {
			if *showDeny {
				fmt.Fprintln(stdout)
			}
			return showRules(stdout, "ignore", ignorePath, hide, nil)
		}
		return nil
	}

	roots, err := resolveRoots(home, fs.Arg(0), extraRoots, *sysRoot)
	if err != nil {
		return err
	}

	g, err := newGuard(roots, deny, hide, *sysRoot)
	if err != nil {
		return err
	}
	// Path rules cannot see a hard link, so files in the credential locations are
	// also recognized by identity. Every home directory on the machine is
	// covered, not only $HOME (which may be wrong or overridden).
	g.Protect(rules.CredentialLocations(home)...)
	g.StartLinkIndex() // in the background; files with several names are refused until it is ready

	// Loopback only. The interface is deliberately not configurable; choosePort
	// only decides which port on it to use (see its own comment).
	ln, err := choosePort(cfgDir, *portFlag, portExplicit)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	var logger *log.Logger
	if *debug {
		logger = log.New(stderr, "fsb: ", log.LstdFlags)
	}
	srv, err := server.New(server.Config{Guard: g, Debug: *debug, CoreDenyMissing: coreMissing, Home: home, Logger: logger})
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(port),
		ReadHeaderTimeout: 10 * time.Second,
	}

	launchURL := fmt.Sprintf("http://127.0.0.1:%d%s?token=%s", port, srv.LaunchPath(), srv.LaunchToken())
	fmt.Fprintf(stdout, "fsb: serving %s read-only on http://127.0.0.1:%d\n", strings.Join(g.Roots(), ", "), port)
	if len(coreMissing) > 0 {
		fmt.Fprintf(stderr, "fsb: warning: core deny rule(s) disabled: %s; these paths are browsable.\n", strings.Join(coreMissing, ", "))
	}
	fmt.Fprintf(stdout, "fsb: open this single-use URL: %s\n", launchURL)
	h, inBackground := stdout.(*handoff)
	if inBackground {
		if err := writePID(cfgDir); err != nil {
			return err
		}
		defer removePID(cfgDir)
	} else {
		fmt.Fprintln(stdout, "fsb: runs until stopped (Control-C), or start it with --background to get your prompt back")
	}
	// Serving before the browser opens: on Linux, opening can take up to
	// openerGrace, while a browser xdg-open started is already asking for the page.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	display := runtime.GOOS == "darwin" || hasDisplay(os.Getenv)
	if name, args := openCommand(runtime.GOOS, *browser, launchURL, display); !*noOpen && name != "" {
		if err := openBrowser(ctx, name, args); err != nil {
			fmt.Fprintf(stderr, "fsb: could not open the browser (%v)\n", err)
			fmt.Fprintln(stderr, "fsb: "+openHint(runtime.GOOS, *browser, err))
		}
	}

	// Stopped while opening the browser: the waiting fsb --background must
	// not hear that this one is running.
	if inBackground && ctx.Err() == nil {
		fmt.Fprintf(stdout, "fsb: running in the background (process %d); stop it with \"fsb --stop\"\n", os.Getpid())
		h.ready()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
