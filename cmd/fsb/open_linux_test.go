package main

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// buildForOpen builds fsb and returns it with a fresh home.
func buildForOpen(t *testing.T) (exe, home string) {
	t.Helper()
	dir := t.TempDir()
	exe = filepath.Join(dir, "fsb")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home = filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return exe, home
}

// openEnv is the environment of an fsb started from a desktop: PATH is bin,
// and there is a display, unless env overrides DISPLAY.
func openEnv(home, bin string, env ...string) []string {
	base := []string{"HOME=" + home, "PATH=" + bin}
	for _, e := range env {
		if strings.HasPrefix(e, "DISPLAY=") {
			return append(base, env...)
		}
	}
	return append(append(base, "DISPLAY=:0"), env...)
}

// foreground is an fsb started in the foreground, as from a terminal (in the
// test's own session).
type foreground struct {
	url    string
	stderr *lockedLines
	cmd    *exec.Cmd
	exited chan struct{}
}

// startForeground starts fsb with PATH set to bin and a display (see openEnv),
// and returns once it has printed that it runs.
func startForeground(t *testing.T, bin string, env []string, args ...string) *foreground {
	t.Helper()
	exe, home := buildForOpen(t)
	cmd := exec.Command(exe, append([]string{"--port", "0"}, args...)...)
	cmd.Env = openEnv(home, bin, env...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &foreground{stderr: &lockedLines{}, cmd: cmd, exited: make(chan struct{})}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		select {
		case <-f.exited:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	})
	go func() {
		s := bufio.NewScanner(errPipe)
		for s.Scan() {
			f.stderr.add(s.Text())
		}
	}()
	go func() { cmd.Wait(); close(f.exited) }()

	lines := make(chan string)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			lines <- s.Text()
		}
		close(lines)
	}()
	re := regexp.MustCompile(`http://127\.0\.0\.1:\d+/\S*`)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("fsb exited before it was ready; stderr: %q", f.stderr.all())
			}
			if m := re.FindString(l); m != "" && f.url == "" {
				f.url = m
			}
			if strings.Contains(l, "runs until stopped") {
				go func() {
					for range lines {
					}
				}()
				return f
			}
		case <-deadline:
			t.Fatalf("fsb did not report it was ready; stderr: %q", f.stderr.all())
		}
	}
}

// fakeOpener writes an executable script called name into a new directory,
// which also links the few tools the scripts use, and returns the directory
// (for PATH) and the file where the script records what it was given.
func fakeOpener(t *testing.T, name, body string) (bin, record string) {
	t.Helper()
	bin = t.TempDir()
	record = filepath.Join(t.TempDir(), "record")
	for _, tool := range []string{"sh", "cat", "cut", "sleep", "readlink"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nrecord=" + record + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

// The opener records, one per line: each argument as arg=, its session
// (field 6 of /proc/$$/stat; $$ is the script's own process), what its
// descriptors 0 to 2 are, whether descriptor 3 is open, the background
// child's marker variable, and its process number. Everything is read before
// the record is written, so the record's own redirection is not seen.
const recordOpener = `session=$(cut -d' ' -f6 /proc/$$/stat)
fd0=$(readlink /proc/$$/fd/0); fd1=$(readlink /proc/$$/fd/1); fd2=$(readlink /proc/$$/fd/2)
if [ -e /proc/$$/fd/3 ]; then fd3=open; else fd3=closed; fi
{ for a in "$@"; do printf 'arg=%s\n' "$a"; done
  printf 'session=%s\nfd0=%s\nfd1=%s\nfd2=%s\nfd3=%s\n' "$session" "$fd0" "$fd1" "$fd2" "$fd3"
  printf 'bgenv=%s\npid=%s\nend\n' "${FSB_BACKGROUND_CHILD-unset}" $$; } > "$record.tmp"
cat "$record.tmp" > "$record"`

// opened is what the fake opener recorded.
type opened struct {
	args []string
	keys map[string]string
}

// waitOpened waits for the opener's record. The process it names (the opener,
// or what it exec'd) is killed when the test ends: it runs in its own session,
// so stopping fsb does not stop it.
func waitOpened(t *testing.T, path string) opened {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(string(b), "end\n") {
			continue
		}
		o := opened{keys: map[string]string{}}
		for _, l := range strings.Split(strings.TrimSuffix(string(b), "end\n"), "\n") {
			k, v, _ := strings.Cut(l, "=")
			if k == "arg" {
				o.args = append(o.args, v)
			} else if k != "" {
				o.keys[k] = v
			}
		}
		if pid, err := strconv.Atoi(o.keys["pid"]); err == nil {
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
		}
		return o
	}
	t.Fatalf("the opener was not run (no %s)", path)
	return opened{}
}

// notOpened checks that the opener has not run, after fsb's grace period.
func notOpened(t *testing.T, path string) {
	t.Helper()
	time.Sleep(openerGrace + time.Second)
	if _, err := os.Stat(path); err == nil {
		t.Errorf("the opener was run")
	}
}

func ownSession(t *testing.T) string {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b)[strings.LastIndexByte(string(b), ')')+2:])
	return f[3] // state ppid pgrp session
}

func TestLinuxOpensTheDefaultBrowserWithXdgOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener)
	f := startForeground(t, bin, nil)
	o := waitOpened(t, record)
	if len(o.args) != 1 || o.args[0] != f.url {
		t.Fatalf("xdg-open was given %q, want the URL %q alone", o.args, f.url)
	}
	// In its own session, so Control-C in fsb's terminal does not also close
	// the browser that xdg-open may have started.
	if o.keys["session"] == ownSession(t) {
		t.Errorf("the opener runs in fsb's session %s; it needs its own", o.keys["session"])
	}
	// No terminal input or output: a browser's messages would fill the
	// terminal, and writing to it after fsb exits could stop the browser.
	for _, fd := range []string{"fd0", "fd1", "fd2"} {
		if o.keys[fd] != "/dev/null" {
			t.Errorf("the opener's %s is %q, want /dev/null", fd, o.keys[fd])
		}
	}
}

func TestLinuxBrowserFlagRunsThatProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "my-browser", recordOpener)
	f := startForeground(t, bin, nil, "--browser", "my-browser")
	if o := waitOpened(t, record); len(o.args) != 1 || o.args[0] != f.url {
		t.Fatalf("--browser program was given %q, want the URL %q alone", o.args, f.url)
	}
}

// Without a display (over ssh, say) there is no graphical browser to open,
// and xdg-open would fall back to a text browser such as w3m, which fetches
// the single-use URL itself and leaves the printed one used up. A Wayland
// display is a display; an explicit --browser is run regardless.
func TestLinuxWithoutADisplayXdgOpenIsNotRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener)
	f := startForeground(t, bin, []string{"DISPLAY="})
	notOpened(t, record)
	if e := f.stderr.all(); len(e) != 0 {
		t.Errorf("no display is not an error, but fsb said %q", e)
	}
}

func TestLinuxAWaylandDisplayIsADisplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener)
	startForeground(t, bin, []string{"DISPLAY=", "WAYLAND_DISPLAY=wayland-0"})
	waitOpened(t, record)
}

func TestLinuxBrowserFlagIsRunWithoutADisplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "my-browser", recordOpener)
	startForeground(t, bin, []string{"DISPLAY="}, "--browser", "my-browser")
	waitOpened(t, record)
}

// A browser started by xdg-open (or named by --browser) may stay in the
// foreground until it is closed; fsb must serve meanwhile, not wait for it.
func TestLinuxABrowserThatKeepsRunningDoesNotHoldUpFsb(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener+"\nexec sleep 60")
	f := startForeground(t, bin, nil)
	waitOpened(t, record)
	// An HTTP answer, not just a connection (the port accepts connections as
	// soon as it is bound), and at once: the browser asks for the page while
	// fsb is still waiting out openerGrace.
	host := strings.TrimPrefix(f.url, "http://")
	host = host[:strings.IndexByte(host, '/')]
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + host + "/")
	if err != nil {
		t.Fatalf("fsb does not answer while the browser runs: %v", err)
	}
	resp.Body.Close()
	time.Sleep(openerGrace + time.Second)
	if e := f.stderr.all(); len(e) != 0 {
		t.Errorf("a browser still running is not a failure, but fsb said %q", e)
	}
}

// Control-C while fsb waits out the opener's grace period stops fsb at once.
func TestLinuxControlCDuringTheOpenerWaitStopsFsbAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener+"\nexec sleep 60")
	f := startForeground(t, bin, nil)
	waitOpened(t, record)
	f.cmd.Process.Signal(os.Interrupt)
	select {
	case <-f.exited:
	case <-time.After(time.Second):
		t.Fatalf("fsb still runs 1 s after Control-C (the grace period is %v)", openerGrace)
	}
}

func TestLinuxAFailedOpenIsReportedAndFsbKeepsServing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	// xdg-open's own status when a tool it needs is missing.
	bin, _ := fakeOpener(t, "xdg-open", "exit 3")
	f := startForeground(t, bin, nil)
	waitLine(t, f.stderr, "could not open the browser")
	waitLine(t, f.stderr, "open the URL above yourself")
}

// The hint names what is missing: xdg-open comes with xdg-utils, and
// --browser was not given.
func TestLinuxMissingXdgOpenIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	f := startForeground(t, t.TempDir(), nil)
	waitLine(t, f.stderr, "could not open the browser")
	waitLine(t, f.stderr, "xdg-utils")
	if strings.Contains(strings.Join(f.stderr.all(), "\n"), "--browser") {
		t.Errorf("no --browser was given, but the hint names it: %q", f.stderr.all())
	}
}

// The background fsb hands its startup output to the waiting fsb through
// descriptor 3 and knows itself by FSB_BACKGROUND_CHILD. Neither may reach
// the browser: an fsb later started from the browser's process tree would
// take itself for a background child (printing nothing, taking over the PID
// file), and a browser holding the pipe keeps `fsb --background` waiting.
func TestLinuxBackgroundOpenerGetsNoHandoffPipeOrMarker(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener+"\nexec sleep 60")
	exe, home := buildForOpen(t)
	fsb := func(args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = openEnv(home, bin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() { fsb("--stop") })
	if out, err := fsb("--background", "--port", "0"); err != nil {
		t.Fatalf("--background: %v\n%s", err, out)
	}
	o := waitOpened(t, record)
	if o.keys["fd3"] != "closed" {
		t.Errorf("the opener holds the handoff pipe (descriptor 3)")
	}
	if o.keys["bgenv"] != "unset" {
		t.Errorf("the opener has FSB_BACKGROUND_CHILD=%q", o.keys["bgenv"])
	}
}

// `fsb --stop` while the background fsb waits out the opener's grace period:
// the waiting `fsb --background` must not then say it is running.
func TestLinuxStopDuringTheOpenerWaitIsNotReportedAsRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordOpener+"\nexec sleep 60")
	exe, home := buildForOpen(t)
	run := func(args ...string) *exec.Cmd {
		cmd := exec.Command(exe, args...)
		cmd.Env = openEnv(home, bin)
		return cmd
	}
	bg := run("--background", "--port", "0")
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() { out, err := bg.CombinedOutput(); done <- result{out, err} }()
	waitOpened(t, record)
	if out, err := run("--stop").CombinedOutput(); err != nil {
		t.Fatalf("--stop: %v\n%s", err, out)
	}
	select {
	case r := <-done:
		if r.err == nil || strings.Contains(string(r.out), "running in the background") {
			t.Errorf("fsb --background reported a stopped fsb as running (%v):\n%s", r.err, r.out)
		}
	case <-time.After(openerGrace + 5*time.Second):
		t.Fatal("fsb --background did not return")
	}
}

func waitLine(t *testing.T, l *lockedLines, want string) {
	t.Helper()
	for end := time.Now().Add(openerGrace + 5*time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(strings.Join(l.all(), "\n"), want) {
			return
		}
	}
	t.Errorf("standard error never said %q; it said %q", want, l.all())
}

// lockedLines collects lines written by one goroutine and read by another.
type lockedLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *lockedLines) add(s string) { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }

func (l *lockedLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
