//go:build !linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// On macOS (and other systems without Linux's /proc), processes and sockets
// are looked up with ps and lsof, which macOS always has.

// executablePath returns the file the process pid was started from.
func executablePath(pid int) (string, bool) {
	out, _ := exec.Command("lsof", "-nP", "-p", strconv.Itoa(pid), "-a", "-d", "txt", "-Fn").Output()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "n"); ok && p != "" {
			return p, true // the first text file is the executable
		}
	}
	return "", false
}

// lookupProcess describes the process pid, or reports that there is none.
func lookupProcess(pid int) (process, bool) {
	if pid <= 1 {
		return process{}, false
	}
	// comm= last: it may contain spaces.
	out, err := exec.Command("ps", "-o", "uid=,tty=,lstart=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return process{}, false
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) < 8 { // uid, tty, five lstart fields, command
		return process{}, false
	}
	uid, err := strconv.Atoi(f[0])
	if err != nil {
		return process{}, false
	}
	p := process{pid: pid, uid: uid, tty: f[1], started: strings.Join(f[2:7], " "), name: filepath.Base(strings.Join(f[7:], " "))}
	if p.tty == "??" || p.tty == "?" || p.tty == "-" { // no terminal (macOS, Linux)
		p.tty = ""
	}
	// ps prints the start time in the local time zone, so read it in the same one.
	if t, err := time.ParseInLocation(lstartLayout, p.started, time.Local); err == nil {
		p.startID = fmt.Sprintf("unix:%d", t.Unix())
	}
	return p, true
}

// portHolder returns the process listening on 127.0.0.1:port, if lsof can see
// one. A process of another user is not visible without privileges.
func portHolder(port int) (process, bool) {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fp").Output()
	if err != nil && len(out) == 0 {
		return process{}, false
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if pid, err := strconv.Atoi(strings.TrimPrefix(sc.Text(), "p")); err == nil && strings.HasPrefix(sc.Text(), "p") {
			return lookupProcess(pid)
		}
	}
	return process{}, false
}

// listeningPort returns the loopback port the process pid listens on, if lsof
// can see one.
func listeningPort(pid int) (int, bool) {
	out, _ := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if addr, ok := strings.CutPrefix(sc.Text(), "n127.0.0.1:"); ok {
			if n, err := strconv.Atoi(addr); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
