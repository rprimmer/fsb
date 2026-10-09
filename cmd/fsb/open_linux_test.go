package main

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// startForeground builds fsb and starts it in the foreground (as from a
// terminal, in the test's own session) with PATH set to bin and the given
// extra arguments. It returns the single-use URL fsb printed and the lines it
// writes to standard error.
func startForeground(t *testing.T, bin string, args ...string) (url string, stderr *lockedLines) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "fsb")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, append([]string{"--port", "0"}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin}
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
	exited := make(chan struct{})
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	})
	stderr = &lockedLines{}
	go func() {
		s := bufio.NewScanner(errPipe)
		for s.Scan() {
			stderr.add(s.Text())
		}
	}()
	go func() { cmd.Wait(); close(exited) }()

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
				t.Fatalf("fsb exited before it was ready; stderr: %q", stderr.all())
			}
			if m := re.FindString(l); m != "" && url == "" {
				url = m
			}
			if strings.Contains(l, "runs until stopped") {
				go func() {
					for range lines {
					}
				}()
				return url, stderr
			}
		case <-deadline:
			t.Fatalf("fsb did not report it was ready; stderr: %q", stderr.all())
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
	for _, tool := range []string{"sh", "cat", "cut", "sleep"} {
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

// waitFile waits for the opener's record and returns its lines.
func waitFile(t *testing.T, path string) []string {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "end\n") {
			return strings.Split(strings.TrimSuffix(string(b), "\nend\n"), "\n")
		}
	}
	t.Fatalf("the opener was not run (no %s)", path)
	return nil
}

// The record: the arguments, one per line, then the opener's session ID
// (field 6 of /proc/self/stat; sh's $$ is the script's own process).
const recordArgsAndSession = `for a in "$@"; do printf '%s\n' "$a"; done > "$record.tmp"
cut -d' ' -f6 /proc/$$/stat >> "$record.tmp"
echo end >> "$record.tmp"; cat "$record.tmp" > "$record"`

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
	bin, record := fakeOpener(t, "xdg-open", recordArgsAndSession)
	url, _ := startForeground(t, bin)
	got := waitFile(t, record)
	if len(got) != 2 || got[0] != url {
		t.Fatalf("xdg-open was given %q, want the URL %q alone", got, url)
	}
	// In its own session, so Control-C in fsb's terminal does not also close
	// the browser that xdg-open may have started.
	if got[1] == ownSession(t) {
		t.Errorf("the opener runs in fsb's session %s; it needs its own", got[1])
	}
}

func TestLinuxBrowserFlagRunsThatProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "my-browser", recordArgsAndSession)
	url, _ := startForeground(t, bin, "--browser", "my-browser")
	if got := waitFile(t, record); len(got) != 2 || got[0] != url {
		t.Fatalf("--browser program was given %q, want the URL %q alone", got, url)
	}
}

// A browser started by xdg-open (or named by --browser) may stay in the
// foreground until it is closed; fsb must serve meanwhile, not wait for it.
func TestLinuxABrowserThatKeepsRunningDoesNotHoldUpFsb(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin, record := fakeOpener(t, "xdg-open", recordArgsAndSession+"\nsleep 60")
	url, stderr := startForeground(t, bin)
	waitFile(t, record)
	// An HTTP answer, not just a connection (the port accepts connections as
	// soon as it is bound), and at once: the browser asks for the page while
	// fsb is still waiting out openerGrace.
	host := strings.TrimPrefix(url, "http://")
	host = host[:strings.IndexByte(host, '/')]
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + host + "/")
	if err != nil {
		t.Fatalf("fsb does not answer while the browser runs: %v", err)
	}
	resp.Body.Close()
	time.Sleep(openerGrace + time.Second)
	if e := stderr.all(); len(e) != 0 {
		t.Errorf("a browser still running is not a failure, but fsb said %q", e)
	}
}

func TestLinuxAFailedOpenIsReportedAndFsbKeepsServing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	// xdg-open's own status for "no method available" (e.g. no desktop).
	bin, _ := fakeOpener(t, "xdg-open", "exit 3")
	_, stderr := startForeground(t, bin)
	waitLine(t, stderr, "could not open the browser")
	waitLine(t, stderr, "open the URL above yourself")
}

func TestLinuxMissingXdgOpenIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	_, stderr := startForeground(t, t.TempDir())
	waitLine(t, stderr, "could not open the browser")
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
