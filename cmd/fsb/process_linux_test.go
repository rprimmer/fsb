//go:build linux

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// On Linux, fsb finds processes and ports through /proc, so --status and
// --stop work on systems without ps or lsof (minimal Debian, Fedora and Arch;
// Alpine's BusyBox ps lacks the options fsb needs). These tests run with an
// empty PATH, so no external program can help.
func withoutTools(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestLookupProcessWithoutPs(t *testing.T) {
	withoutTools(t)
	p, ok := lookupProcess(os.Getpid())
	if !ok {
		t.Fatal("lookupProcess found nothing for this process")
	}
	if p.pid != os.Getpid() || p.uid != os.Getuid() {
		t.Errorf("pid, uid = %d, %d; want %d, %d", p.pid, p.uid, os.Getpid(), os.Getuid())
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(self)
	want = want[:min(15, len(want))] // the kernel keeps 15 bytes of the name
	if p.name != want {
		t.Errorf("name = %q, want %q", p.name, want)
	}
	started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", p.started, time.Local)
	if err != nil {
		t.Fatalf("started = %q: %v", p.started, err)
	}
	if age := time.Since(started); age < -time.Minute || age > time.Hour {
		t.Errorf("started = %q, %v ago; this test began moments ago", p.started, age)
	}
	again, _ := lookupProcess(os.Getpid())
	if again.started != p.started {
		t.Errorf("start time changed between lookups: %q, then %q", p.started, again.started)
	}
}

func TestLookupProcessOfNoProcess(t *testing.T) {
	withoutTools(t)
	if p, ok := lookupProcess(1 << 30); ok {
		t.Errorf("lookupProcess(1<<30) = %v, want none", p)
	}
}

func TestPortHolderAndListeningPortWithoutLsof(t *testing.T) {
	withoutTools(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	p, ok := portHolder(port)
	if !ok || p.pid != os.Getpid() {
		t.Errorf("portHolder(%d) = %v, %v; want this process (%d)", port, p, ok, os.Getpid())
	}
	if n, ok := listeningPort(os.Getpid()); !ok || n != port {
		t.Errorf("listeningPort(self) = %d, %v; want %d", n, ok, port)
	}
	ln.Close()
	if p, ok := portHolder(port); ok {
		t.Errorf("portHolder(%d) after close = %v, want none", port, p)
	}
}

func TestParseStatHandlesAnyCommandName(t *testing.T) {
	// The name is in parentheses and may itself contain ") (" and spaces.
	line := "4242 (a) (b c) S 1 4242 4242 34817 4242 4194560 1 0 0 0 0 0 0 0 20 0 1 0 98765 0 0"
	s, ok := parseStat(line)
	if !ok {
		t.Fatal("parseStat failed")
	}
	if s.name != "a) (b c" || s.tty != "pts/1" || s.start != 98765 {
		t.Errorf("parseStat = %+v; want name %q, tty pts/1, start 98765", s, "a) (b c")
	}
}

func TestTTYName(t *testing.T) {
	for _, c := range []struct {
		nr   uint64
		want string
	}{
		{0, ""},
		{136<<8 | 0, "pts/0"},
		{136<<8 | 5, "pts/5"},
		{137<<8 | 2, "pts/258"},
		{4<<8 | 1, "tty1"},
		{4<<8 | 64, "ttyS0"},
		{136<<8 | 0xff | 1<<20, "pts/511"}, // minor 0x1ff: bits above the low 8 are stored from bit 20
	} {
		if got := ttyName(c.nr); got != c.want {
			t.Errorf("ttyName(%#x) = %q, want %q", c.nr, got, c.want)
		}
	}
}

// Rebuilding fsb (make) while it runs replaces its file, and Linux then names
// the executable "<path> (deleted)"; --stop must still recognize it.
func TestExecutablePathOfAReplacedFile(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep program")
	}
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "fsb")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	withoutTools(t)
	path, ok := executablePath(cmd.Process.Pid)
	if !ok || path != bin {
		t.Errorf("executablePath = %q, %v; want %q", path, ok, bin)
	}
	p, ok := lookupProcess(cmd.Process.Pid)
	if !ok || !p.isFsb() {
		t.Errorf("a running fsb whose file was replaced is not recognized: %v, %v", p, ok)
	}
}
