package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// From the independent review of 2026-10-08: the PID file recorded the start
// time as local-time text, so an fsb started under one TZ was not found, and its
// PID file was deleted, by --stop or --status under another.  findRunning finds
// this test process itself (its executable is the running binary).
func TestARunningFsbIsFoundUnderAnotherTimeZone(t *testing.T) {
	cfg := t.TempDir()
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	setTZ := func(name string) {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Skipf("no time zone data for %s: %v", name, err)
		}
		t.Setenv("TZ", name) // for ps, on macOS
		time.Local = loc
	}

	setTZ("UTC")
	if err := writePID(cfg); err != nil {
		t.Fatal(err)
	}
	setTZ("Asia/Tokyo")
	p, background, ok := findRunning(cfg)
	if !ok || !background || p.pid != os.Getpid() {
		t.Fatalf("findRunning under another TZ = %v, %v, %v; want this process, in the background", p, background, ok)
	}
	if _, err := os.Stat(filepath.Join(cfg, pidFile)); err != nil {
		t.Errorf("the PID file is gone: %v", err)
	}
}

// A PID file written by an earlier version holds the start time as text; it
// is still honored when the time zone has not changed.
func TestAnOlderPIDFileIsStillRead(t *testing.T) {
	cfg := t.TempDir()
	p, ok := lookupProcess(os.Getpid())
	if !ok {
		t.Fatal("cannot describe this process")
	}
	if err := os.WriteFile(filepath.Join(cfg, pidFile), []byte(strconv.Itoa(os.Getpid())+"\n"+p.started+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := findRunning(cfg); !ok {
		t.Error("an older PID file (start time as text) is no longer honored")
	}
}
