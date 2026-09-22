// Command fsb serves a read-only, local-only web view of your filesystem.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fsb:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("fsb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	var (
		extraRoots stringList
		debug      = fs.Bool("debug", false, "verbose logging; denied paths answer 404 naming the matching rule")
		noOpen     = fs.Bool("no-open", false, "do not open the browser; just print the URL")
		browser    = fs.String("browser", "", `macOS application to open the URL in, e.g. "Google Chrome" (default: your default browser)`)
		doInit     = fs.Bool("init", false, "write the default ignore and deny files to ~/.config/fsb and exit")
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

	roots, err := resolveRoots(home, fs.Arg(0), extraRoots, *sysRoot)
	if err != nil {
		return err
	}

	// A malformed rules file is fatal: never start with weaker rules than the
	// user wrote.
	deny, coreMissing, err := rules.LoadDeny(filepath.Join(cfgDir, "deny"), home)
	if err != nil {
		return fmt.Errorf("deny rules: %w", err)
	}
	hide, err := rules.LoadIgnore(filepath.Join(cfgDir, "ignore"), home)
	if err != nil {
		return fmt.Errorf("ignore rules: %w", err)
	}
	g, err := guard.New(roots, deny, hide)
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
	if !*noOpen && runtime.GOOS == "darwin" {
		// The URL is passed as one argument, never through a shell. `open` returns
		// promptly, so its exit status says whether the application was found.
		name, args := openCommand(*browser, launchURL)
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			fmt.Fprintf(stderr, "fsb: could not open the browser (%v): %s\n", err, strings.TrimSpace(string(out)))
			fmt.Fprintln(stderr, "fsb: open the URL above yourself, or check the --browser name")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
