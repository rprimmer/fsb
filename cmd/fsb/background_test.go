package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The real binary is built and run, since --background starts a second
// process.
func TestBackgroundStartsDetachedAndStopStopsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fsb")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fsb := func(args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() { fsb("--stop") })

	out, err := fsb("--background", "--no-open", "--port", "0")
	if err != nil {
		t.Fatalf("--background: %v\n%s", err, out)
	}
	if !strings.Contains(out, "open this single-use URL: http://127.0.0.1:") {
		t.Errorf("no URL printed:\n%s", out)
	}
	m := regexp.MustCompile(`background \(process (\d+)\)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no process number printed:\n%s", out)
	}
	running, _, _ := findRunning(filepath.Join(home, ".config", "fsb"))
	pid := running.pid
	if want := m[1]; want != itoa(pid) {
		t.Fatalf("PID file names %d, output names %s", pid, want)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the background fsb is not running after the parent returned")
	}

	if out, err := fsb("--background", "--no-open", "--port", "0"); err == nil || !strings.Contains(out, "already running") {
		t.Errorf("a second --background should be refused: %v\n%s", err, out)
	}
	if out, _ := fsb("--status"); !strings.Contains(out, "running in the background on http://127.0.0.1:") || !strings.Contains(out, "process "+m[1]) {
		t.Errorf("--status:\n%s", out)
	}
	if out, err := fsb("--stop"); err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("--stop: %v\n%s", err, out)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Error("still running after --stop")
	}
	if out, _ := fsb("--status"); !strings.Contains(out, "not running") {
		t.Errorf("--status after --stop:\n%s", out)
	}
	if out, err := fsb("--stop"); err == nil || !strings.Contains(out, "no fsb is running") {
		t.Errorf("--stop with nothing running: %v\n%s", err, out)
	}

	// A startup failure in the child is shown once, and is a failure.
	out, err = fsb("--background", "--no-open", filepath.Join(dir, "missing"))
	if err == nil || strings.Count(out, "no such file") != 1 {
		t.Errorf("startup error not reported exactly once: %v\n%s", err, out)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Today's case: an fsb started in another terminal (not --background) holds
// the usual port. A second start names it, --status finds it, --stop stops it.
func TestForegroundFsbOnTheUsualPortIsNamedAndStoppable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fsb")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+home)
	fsb := func(args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	first := exec.Command(exe, "--no-open")
	first.Env = env
	stdout, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap it as its shell would, or it lingers as a zombie --stop would wait on.
	exited := make(chan error, 1)
	go func() { exited <- first.Wait() }()
	t.Cleanup(func() { first.Process.Kill() })
	buf := make([]byte, 4096)
	var got string
	for !strings.Contains(got, "runs until stopped") {
		n, err := stdout.Read(buf)
		if err != nil {
			t.Fatalf("first fsb did not start: %v\n%s", err, got)
		}
		got += string(buf[:n])
	}
	pid := itoa(first.Process.Pid)

	out, err := fsb("--no-open")
	if err == nil || !strings.Contains(out, "in use by another fsb") || !strings.Contains(out, "process "+pid) || !strings.Contains(out, "fsb --stop") {
		t.Errorf("second start should name the first: %v\n%s", err, out)
	}
	if out, _ := fsb("--status"); !strings.Contains(out, "running in the foreground on http://127.0.0.1:") || !strings.Contains(out, "process "+pid) {
		t.Errorf("--status:\n%s", out)
	}
	if out, err := fsb("--stop"); err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("--stop: %v\n%s", err, out)
	}
	if err := <-exited; err != nil {
		t.Errorf("first fsb should exit cleanly on --stop: %v", err)
	}
}

// buildFsb builds the binary and returns it with a fresh home and a runner.
func buildFsb(t *testing.T) (home string, fsb func(...string) (string, error)) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "fsb")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home = filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "fsb"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home, func(args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

// From the independent review: a process that only calls itself fsb (its
// argv[0], which ps shows on macOS) must never be signaled by --stop, whether
// it is named in the PID file or listening on the remembered port.
func TestStopNeverSignalsAProcessThatOnlyCallsItselfFsb(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	home, fsb := buildFsb(t)
	cfg := filepath.Join(home, ".config", "fsb")
	survives := func(cmd *exec.Cmd, what string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		out, err := fsb("--stop")
		select {
		case e := <-done:
			t.Errorf("%s was stopped (%v); --stop said: %v %s", what, e, err, out)
		case <-time.After(time.Second):
			cmd.Process.Kill()
			<-done
		}
	}

	imp := decoy(t, "")
	// With its real start time, as writePID records it, so that only the check
	// that it is fsb can refuse it (without one, the start time refuses it first).
	p, ok := lookupProcess(imp.Process.Pid)
	if !ok || p.startID == "" {
		t.Fatalf("cannot describe the decoy (process %d)", imp.Process.Pid)
	}
	os.WriteFile(filepath.Join(cfg, "pid"), []byte(strconv.Itoa(imp.Process.Pid)+"\n"+p.startID+"\n"), 0o600)
	survives(imp, "a program named fsb, in the PID file")
	os.Remove(filepath.Join(cfg, "pid"))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	listening := decoy(t, port)
	os.WriteFile(filepath.Join(cfg, "lastport"), []byte(port+"\n"), 0o600)
	if out, _ := fsb("--status"); !strings.Contains(out, "not running") {
		t.Errorf("--status took a program named fsb for fsb:\n%s", out)
	}
	survives(listening, "a program named fsb, on the remembered port")
}

// decoy starts a program that only calls itself fsb: this test binary, with
// argv[0] set to an fsb path, which ps shows as its name on macOS. Given a
// port, it listens on it at 127.0.0.1. It returns once the program is ready.
// (Not /bin/sleep or nc: on BusyBox systems those are one program that acts
// on its argv[0], and exits at once when that is "fsb".)
func decoy(t *testing.T, port string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{
		Path: self,
		Args: []string{"/opt/whatever/fsb", "-test.run=^TestDecoyProcess$"},
		Env:  append(os.Environ(), "FSB_TEST_DECOY=1", "FSB_TEST_DECOY_PORT="+port),
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if n, _ := io.ReadFull(out, buf); string(buf[:n]) != "ready\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("the decoy did not start (said %q)", buf[:n])
	}
	return cmd
}

// TestDecoyProcess is the program decoy starts; on its own it does nothing.
func TestDecoyProcess(t *testing.T) {
	if os.Getenv("FSB_TEST_DECOY") != "1" {
		t.Skip("run by decoy")
	}
	if port := os.Getenv("FSB_TEST_DECOY_PORT"); port != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			os.Exit(1)
		}
		defer ln.Close()
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(time.Minute)
	os.Exit(0)
}

// From the independent review: fsb's own files are never written through a
// symbolic link, so a link planted in ~/.config/fsb cannot make it overwrite
// another file.
func TestOwnFilesAreNeverWrittenThroughASymlink(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	home, fsb := buildFsb(t)
	cfg := filepath.Join(home, ".config", "fsb")
	victims := map[string]string{}
	for _, name := range []string{"background.log", "pid", "lastport"} {
		v := filepath.Join(home, "victim-"+name)
		os.WriteFile(v, []byte("IMPORTANT DATA\n"), 0o644)
		os.Symlink(v, filepath.Join(cfg, name))
		victims[name] = v
	}
	os.MkdirAll(filepath.Join(home, "docs"), 0o755)
	t.Cleanup(func() { fsb("--stop") })
	fsb("--background", "--no-open", filepath.Join(home, "docs"))
	for name, v := range victims {
		if b, _ := os.ReadFile(v); string(b) != "IMPORTANT DATA\n" {
			t.Errorf("the target of a link named %s was overwritten: %q", name, b)
		}
	}
}

// From the review's hardening suggestions: an own file that is another name
// for some other file (a hard link) must not be truncated or written, and a
// FIFO planted in its place must not make fsb hang waiting for a reader.
func TestOwnFilesAreNeverWrittenThroughAHardLinkOrIntoAFIFO(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("IMPORTANT DATA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "pid")
	if err := os.Link(victim, linked); err != nil {
		t.Skip("hard links not supported here:", err)
	}
	if err := writeOwnFile(linked, "123\n"); err == nil {
		t.Error("wrote through a hard link")
	}
	if b, _ := os.ReadFile(victim); string(b) != "IMPORTANT DATA\n" {
		t.Errorf("the other name's file was changed: %q", b)
	}

	fifo := filepath.Join(dir, "lastport")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writeOwnFile(fifo, "8080\n") }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("wrote into a FIFO")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writing an own file blocked on a FIFO")
	}

	// An ordinary own file is replaced, and left private.
	own := filepath.Join(dir, "background.log")
	if err := os.WriteFile(own, []byte("old contents, longer than the new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnFile(own, "new\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(own); string(b) != "new\n" {
		t.Errorf("contents = %q", b)
	}
	if fi, _ := os.Stat(own); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}
