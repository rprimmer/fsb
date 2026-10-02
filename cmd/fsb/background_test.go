package main

import (
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

	imp := &exec.Cmd{Path: "/bin/sleep", Args: []string{"/opt/whatever/fsb", "60"}}
	if err := imp.Start(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cfg, "pid"), []byte(strconv.Itoa(imp.Process.Pid)+"\n"), 0o600)
	survives(imp, "a sleep named fsb, in the PID file")
	os.Remove(filepath.Join(cfg, "pid"))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	nc := &exec.Cmd{Path: "/usr/bin/nc", Args: []string{"fsb", "-l", "127.0.0.1", strconv.Itoa(port)}}
	if err := nc.Start(); err != nil {
		t.Skipf("nc: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	os.WriteFile(filepath.Join(cfg, "lastport"), []byte(strconv.Itoa(port)+"\n"), 0o600)
	if out, _ := fsb("--status"); !strings.Contains(out, "not running") {
		t.Errorf("--status took nc named fsb for fsb:\n%s", out)
	}
	survives(nc, "nc named fsb, on the remembered port")
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
