package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
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
	pid, _ := runningPID(filepath.Join(home, ".config", "fsb"))
	if want := m[1]; want != itoa(pid) {
		t.Fatalf("PID file names %d, output names %s", pid, want)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the background fsb is not running after the parent returned")
	}

	if out, err := fsb("--background", "--no-open", "--port", "0"); err == nil || !strings.Contains(out, "already running") {
		t.Errorf("a second --background should be refused: %v\n%s", err, out)
	}
	if out, err := fsb("--stop"); err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("--stop: %v\n%s", err, out)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Error("still running after --stop")
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
