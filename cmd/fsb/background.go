package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// --background starts a second copy of fsb (the "child") detached from the
// terminal and waits only until it is serving. The child is told what it is by
// childEnv, and reports its startup output through a pipe on file descriptor 3
// (the "handoff"), ending with readyMarker once it is serving. Anything it
// writes after that goes to backgroundLog. Its process ID is kept in pidFile so
// that fsb --stop can find it.
const (
	childEnv      = "FSB_BACKGROUND_CHILD"
	readyMarker   = "\x00fsb-ready"
	pidFile       = "pid"
	backgroundLog = "background.log"
)

func isBackgroundChild() bool { return os.Getenv(childEnv) == "1" }

// handoff is the child's stdout and stderr: the parent's pipe until the child
// is serving, its log file after that.
type handoff struct {
	mu   sync.Mutex
	pipe io.WriteCloser // nil once handed off
	log  io.Writer
}

func (h *handoff) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pipe != nil {
		return h.pipe.Write(p)
	}
	return h.log.Write(p)
}

// ready tells the parent that startup succeeded and closes the pipe, so the
// parent can exit while the child keeps serving.
func (h *handoff) ready() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pipe != nil {
		io.WriteString(h.pipe, readyMarker+"\n")
		h.pipe.Close()
		h.pipe = nil
	}
}

// childMain runs fsb as the background child and returns its exit status.
func childMain(args []string) int {
	pipe := os.NewFile(3, "handoff")
	var log io.Writer = io.Discard
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".config", "fsb")
		if os.MkdirAll(dir, 0o700) == nil {
			if f, err := os.OpenFile(filepath.Join(dir, backgroundLog), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600); err == nil {
				log = f
			}
		}
	}
	h := &handoff{pipe: pipe, log: log}
	if err := run(args, h, h); err != nil {
		fmt.Fprintln(h, "fsb:", err)
		return 1
	}
	return 0
}

// startBackground starts the child with the same arguments and copies its
// startup output to stdout until it is serving (success) or has exited (its
// error is shown, and returned as a failure).
func startBackground(cfgDir string, args []string, stdout io.Writer) error {
	if pid, ok := runningPID(cfgDir); ok {
		return fmt.Errorf("fsb is already running in the background (process %d); stop it with fsb --stop", pid)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.ExtraFiles = []*os.File{w}
	// A new session: the child has no controlling terminal, so closing the
	// terminal or pressing Control-C there does not stop it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		w.Close()
		return err
	}
	w.Close() // only the child holds the write end now, so its exit ends the read loop

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if sc.Text() == readyMarker {
			return cmd.Process.Release()
		}
		fmt.Fprintln(stdout, sc.Text())
	}
	// The pipe closed without the marker: the child failed and has printed why.
	err = cmd.Wait()
	if err == nil {
		err = errors.New("the background fsb exited before it was ready")
	}
	return errAlreadyReported{err}
}

// errAlreadyReported is an error whose message has already been shown, so
// main exits with failure without printing it again.
type errAlreadyReported struct{ error }

// writePID records this process as the background fsb.
func writePID(cfgDir string) error {
	return os.WriteFile(filepath.Join(cfgDir, pidFile), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

// removePID removes the PID file if it still names this process.
func removePID(cfgDir string) {
	p := filepath.Join(cfgDir, pidFile)
	if data, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid()) {
		os.Remove(p)
	}
}

// runningPID returns the process ID of the background fsb, if one is running.
// A PID file left behind by a process that has since gone, or whose number now
// belongs to some other program, does not count.
func runningPID(cfgDir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(cfgDir, pidFile))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return 0, false
	}
	exe, _ := os.Executable()
	name := filepath.Base(strings.TrimSpace(string(out)))
	if name != "fsb" && name != filepath.Base(exe) {
		return 0, false
	}
	return pid, true
}

// stopBackground stops the background fsb and waits briefly for it to exit.
func stopBackground(cfgDir string, stdout io.Writer) error {
	pid, ok := runningPID(cfgDir)
	if !ok {
		if err := os.Remove(filepath.Join(cfgDir, pidFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return errors.New("no fsb is running in the background")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping process %d: %w", pid, err)
	}
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			fmt.Fprintf(stdout, "fsb: stopped the background fsb (process %d)\n", pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not stop within 5 seconds", pid)
}
