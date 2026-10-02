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
			if f, err := openOwnFile(filepath.Join(dir, backgroundLog)); err == nil {
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
	if p, _, ok := findRunning(cfgDir); ok {
		return fmt.Errorf("fsb is already running: %s; stop it with \"fsb --stop\"", p)
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

// writePID records this process as the background fsb: its number, and when
// it started, so that a number later reused by another process is not
// mistaken for it.
func writePID(cfgDir string) error {
	started := ""
	if p, ok := lookupProcess(os.Getpid()); ok {
		started = p.started
	}
	return writeOwnFile(filepath.Join(cfgDir, pidFile), strconv.Itoa(os.Getpid())+"\n"+started+"\n")
}

// readPID returns the process number and start time recorded by writePID.
func readPID(cfgDir string) (pid int, started string, ok bool) {
	data, err := os.ReadFile(filepath.Join(cfgDir, pidFile))
	if err != nil {
		return 0, "", false
	}
	lines := strings.SplitN(string(data), "\n", 3)
	pid, err = strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0, "", false
	}
	if len(lines) > 1 {
		started = strings.TrimSpace(lines[1])
	}
	return pid, started, true
}

// openOwnFile opens one of fsb's own files for writing, empty, with mode 0600
// whether or not it existed. Whatever could plant something in ~/.config/fsb
// must not make fsb change another file, or hang, through it, so it refuses
// anything but an ordinary file of its own: a symbolic link (never followed),
// a hard link (another name for some other file, checked before anything is
// truncated), a FIFO or device (opened without blocking), or a file owned by
// someone else. Links in the folders above it are not checked: whoever can
// change those already controls the user's files.
func openOwnFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		f.Close()
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return fail(fmt.Errorf("%s is not an ordinary file; remove it", path))
	case !ok || st.Nlink != 1:
		return fail(fmt.Errorf("%s has other names (a hard link); remove it", path))
	case int(st.Uid) != os.Getuid():
		return fail(fmt.Errorf("%s belongs to another user; remove it", path))
	}
	if err := f.Truncate(0); err != nil {
		return fail(err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	return f, nil
}

// writeOwnFile replaces the contents of one of fsb's own files (openOwnFile).
func writeOwnFile(path, content string) error {
	f, err := openOwnFile(path)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// removePID removes the PID file if it still names this process.
func removePID(cfgDir string) {
	if pid, _, ok := readPID(cfgDir); ok && pid == os.Getpid() {
		os.Remove(filepath.Join(cfgDir, pidFile))
	}
}

// findRunning finds a running fsb: the one started with --background (from
// the PID file), or else one, started any way, that is listening on the port
// remembered in lastport. A PID file left behind by a process that has since
// gone, or whose number now belongs to some other program, does not count, and
// neither does a program other than fsb holding the port.
func findRunning(cfgDir string) (p process, background, ok bool) {
	if pid, started, ok := readPID(cfgDir); ok {
		if p, ok := lookupProcess(pid); ok && p.started == started && p.isFsb() {
			return p, true, true
		}
	}
	if port := readLastPort(cfgDir); port != 0 {
		if p, ok := portHolder(port); ok && p.isFsb() {
			return p, false, true
		}
	}
	return process{}, false, false
}

// stopRunning stops the fsb that findRunning finds and waits briefly for it to
// exit.
func stopRunning(cfgDir string, stdout io.Writer) error {
	p, _, ok := findRunning(cfgDir)
	if !ok {
		if err := os.Remove(filepath.Join(cfgDir, pidFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return errors.New("no fsb is running")
	}
	if err := syscall.Kill(p.pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping process %d: %w", p.pid, err)
	}
	for range 50 {
		if syscall.Kill(p.pid, 0) != nil {
			fmt.Fprintf(stdout, "fsb: stopped %s\n", p)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not stop within 5 seconds", p.pid)
}

// showStatus says whether fsb is running, and where.
func showStatus(cfgDir string, stdout io.Writer) {
	p, background, ok := findRunning(cfgDir)
	if !ok {
		fmt.Fprintln(stdout, "fsb: not running")
		return
	}
	how := "in the foreground"
	if background {
		how = "in the background"
	}
	port := ""
	if n, ok := listeningPort(p.pid); ok {
		port = fmt.Sprintf(" on http://127.0.0.1:%d", n)
	}
	fmt.Fprintf(stdout, "fsb: running %s%s: %s; stop it with \"fsb --stop\"\n", how, port, p)
}
