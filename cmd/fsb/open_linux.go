package main

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// openerGrace is how long fsb waits for xdg-open (or the --browser program) to
// fail. A browser it starts may stay in the foreground until it is closed, so
// one still running after this is taken to have opened.
const openerGrace = 3 * time.Second

// openBrowser starts the opener in its own session, so that Control-C in fsb's
// terminal does not also close the browser, with no terminal input or output:
// a browser's messages would otherwise fill the terminal, and writing to it
// after fsb exits could stop the browser. It returns at once when ctx ends
// (Control-C, fsb --stop), leaving the opener to finish on its own.
func openBrowser(ctx context.Context, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(openerGrace):
		return nil
	case <-ctx.Done():
		return nil
	}
}
