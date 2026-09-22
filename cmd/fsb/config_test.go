package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

func TestResolveRoots(t *testing.T) {
	got, err := resolveRoots("/Users/u", "", nil, false)
	if err != nil || len(got) != 1 || got[0] != "/Users/u" {
		t.Fatalf("default root: %v %v", got, err)
	}
	got, err = resolveRoots("/Users/u", "/Users/u/proj/", []string{"/Volumes/X"}, false)
	if err != nil || strings.Join(got, ",") != "/Users/u/proj,/Volumes/X" {
		t.Fatalf("explicit roots: %v %v", got, err)
	}
	for _, args := range [][]string{{"/"}, {"/", ""}, {"//"}, {"/.."}} {
		if _, err := resolveRoots("/Users/u", args[0], nil, false); err == nil {
			t.Errorf("%q must require --allow-system-root", args[0])
		}
	}
	if _, err := resolveRoots("/Users/u", "", []string{"/"}, false); err == nil {
		t.Error("--root / must require --allow-system-root")
	}
	if _, err := resolveRoots("/Users/u", "/", nil, true); err != nil {
		t.Errorf("/ should be allowed with the flag: %v", err)
	}
}

func TestInitWritesActiveCoreRulesAndNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fsb")
	var out bytes.Buffer
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}
	// The written deny file must load cleanly with the core rules active.
	set, missing, err := rules.LoadDeny(filepath.Join(dir, "deny"), "/Users/u")
	if err != nil || len(missing) != 0 {
		t.Fatalf("LoadDeny: missing=%v err=%v", missing, err)
	}
	if !set.Match("/Users/u/.ssh/id_rsa", false).Matched {
		t.Error("core rules must be active in the generated file")
	}
	if !set.Match("/Users/u/proj/.env", false).Matched || !set.Match("/Users/u/keys/server.pem", false).Matched {
		t.Error("the secret-file patterns are core rules and must be active in the generated file")
	}
	if set.Match("/Users/u/.npmrc", false).Matched {
		t.Error("optional rules must be commented out in the generated file")
	}

	// A user's edits survive a second --init.
	deny := filepath.Join(dir, "deny")
	if err := os.WriteFile(deny, []byte("~/.aws/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(deny); string(b) != "~/.aws/\n" {
		t.Errorf("existing deny file was overwritten: %q", b)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("expected an 'already exists' message, got %q", out.String())
	}
}

func TestOpenCommand(t *testing.T) {
	name, args := openCommand("", "http://127.0.0.1:1/?token=x")
	if name != "open" || strings.Join(args, "|") != "http://127.0.0.1:1/?token=x" {
		t.Errorf("default browser: %s %q", name, args)
	}
	name, args = openCommand("Google Chrome", "http://127.0.0.1:1/?token=x")
	if name != "open" || strings.Join(args, "|") != "-a|Google Chrome|http://127.0.0.1:1/?token=x" {
		t.Errorf("named browser: %s %q", name, args)
	}
	// The URL and the name are always single arguments, whatever they contain.
	_, args = openCommand("My Browser; rm -rf ~", "http://x/?a=1&b=2")
	if len(args) != 3 || args[1] != "My Browser; rm -rf ~" || args[2] != "http://x/?a=1&b=2" {
		t.Errorf("arguments must stay whole: %q", args)
	}
}

func TestCheckBrowserName(t *testing.T) {
	for _, ok := range []string{"", "Safari", "Google Chrome", "Firefox", "Brave Browser", "Microsoft Edge"} {
		if err := checkBrowserName(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"-a", "--help", "-g", "Chrome\nSafari", "Chrome\r", "a\x00b"} {
		if err := checkBrowserName(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

// A bad --browser value is refused before anything is started.
func TestRunRejectsABadBrowserNameEarly(t *testing.T) {
	var out, errb bytes.Buffer
	err := run([]string{"--browser", "--help", "--no-open"}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "--browser") {
		t.Fatalf("err = %v, want a --browser error", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing may be started or printed for a bad value, got %q", out.String())
	}
}

func TestRunRejectsAnOutOfRangePortEarly(t *testing.T) {
	for _, bad := range []string{"-1", "65536", "999999"} {
		var out, errb bytes.Buffer
		err := run([]string{"--port", bad, "--no-open"}, &out, &errb)
		if err == nil || !strings.Contains(err.Error(), "--port") {
			t.Errorf("--port %s: err = %v, want a --port error", bad, err)
		}
		if out.Len() != 0 {
			t.Errorf("--port %s: nothing may be started or printed for a bad value, got %q", bad, out.String())
		}
	}
}

func TestReadLastPort(t *testing.T) {
	dir := t.TempDir()
	if got := readLastPort(dir); got != 0 {
		t.Errorf("no file: got %d, want 0", got)
	}
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(dir, lastPortFile), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"", "not a number", "0", "-1", "65536", "999999", "  \n"} {
		write(bad)
		if got := readLastPort(dir); got != 0 {
			t.Errorf("content %q: got %d, want 0", bad, got)
		}
	}
	write("54321\n") // the trailing newline writeLastPort itself produces
	if got := readLastPort(dir); got != 54321 {
		t.Errorf("got %d, want 54321", got)
	}
	write("12345") // no trailing newline: still valid
	if got := readLastPort(dir); got != 12345 {
		t.Errorf("got %d, want 12345", got)
	}
}

func TestWriteLastPortThenReadLastPortRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fsb") // does not exist yet, like a first run with no --init
	writeLastPort(dir, 4488)
	if got := readLastPort(dir); got != 4488 {
		t.Errorf("got %d, want 4488", got)
	}
	writeLastPort(dir, 4499) // a later, successful start updates it
	if got := readLastPort(dir); got != 4499 {
		t.Errorf("got %d, want 4499", got)
	}
}

// occupy binds a real listener on an ephemeral port and returns that port,
// still held, so a test can simulate "this port is taken by another program".
func occupy(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

func TestChoosePortExplicitIsNeverRemembered(t *testing.T) {
	dir := t.TempDir()
	ln, err := choosePort(dir, 0, true) // explicit --port 0: still a real, deliberate choice
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := readLastPort(dir); got != 0 {
		t.Errorf("an explicit --port must not be remembered, got %d", got)
	}

	taken, release := occupy(t)
	defer release()
	if _, err := choosePort(dir, taken, true); err == nil {
		t.Error("binding an explicitly requested, already-taken port must fail")
	}
	if got := readLastPort(dir); got != 0 {
		t.Errorf("a failed explicit choice must not write anything, got %d", got)
	}
}

func TestChoosePortAutoRemembersOnFirstSuccess(t *testing.T) {
	dir := t.TempDir()
	ln, err := choosePort(dir, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if got := readLastPort(dir); got != port {
		t.Errorf("the freshly chosen port must be remembered: got %d, want %d", got, port)
	}
}

func TestChoosePortAutoReusesTheRememberedPort(t *testing.T) {
	dir := t.TempDir()
	first, err := choosePort(dir, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	port := first.Addr().(*net.TCPAddr).Port
	first.Close()

	second, err := choosePort(dir, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := second.Addr().(*net.TCPAddr).Port; got != port {
		t.Errorf("got port %d, want the remembered %d", got, port)
	}
}

func TestChoosePortAutoFailsLoudlyWhenTheRememberedPortIsTaken(t *testing.T) {
	dir := t.TempDir()
	taken, release := occupy(t)
	defer release()
	writeLastPort(dir, taken)

	_, err := choosePort(dir, 0, false)
	if err == nil {
		t.Fatal("expected an error; must not silently fall back to a different port")
	}
	if !strings.Contains(err.Error(), "--port") || !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Errorf("err = %v, want it to name the port and suggest --port", err)
	}
	// The remembered value itself is untouched, so the next attempt (once the
	// conflict clears) goes back to the same port rather than drifting away.
	if got := readLastPort(dir); got != taken {
		t.Errorf("got %d, want the original %d preserved", got, taken)
	}
}
