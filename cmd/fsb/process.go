package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// process describes a running program, as far as fsb's messages need it.
type process struct {
	pid     int
	uid     int
	name    string // command name as ps shows it, without its folder
	started string // as ps prints it, e.g. "Fri Oct  2 06:08:27 2026"
	tty     string // terminal, e.g. "ttys001"; "" if it has none
}

// isFsb reports whether p is an fsb of this user: a process of the same user
// whose executable file is named fsb, or has this program's own name. The name
// ps shows is not used for this, because on macOS it is the process's argv[0],
// which any program can set; the executable file is what the kernel loaded.
// (Another program of the same user, copied under the name fsb, would still
// pass; that user can signal it anyway.)
func (p process) isFsb() bool {
	if p.uid != os.Getuid() {
		return false
	}
	path, ok := executablePath(p.pid)
	if !ok {
		return false
	}
	self, _ := os.Executable()
	base := filepath.Base(path)
	return base == "fsb" || (self != "" && base == filepath.Base(self))
}

// executablePath returns the file the process pid was started from.
func executablePath(pid int) (string, bool) {
	if p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil { // Linux
		return p, true
	}
	out, _ := exec.Command("lsof", "-nP", "-p", strconv.Itoa(pid), "-a", "-d", "txt", "-Fn").Output()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "n"); ok && p != "" {
			return p, true // the first text file is the executable
		}
	}
	return "", false
}

func (p process) String() string {
	s := fmt.Sprintf("%s (process %d", p.name, p.pid)
	if p.started != "" {
		s += ", started " + p.started
	}
	if p.tty != "" {
		s += ", in terminal " + p.tty
	}
	return s + ")"
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

// portInUseMessage explains that the remembered port is taken, naming the
// program that holds it when that can be found.
func portInUseMessage(port int) string {
	p, ok := portHolder(port)
	switch {
	case !ok:
		return fmt.Sprintf("port %d (used last time) is already in use by another program; run again with --port N to use a different one", port)
	case p.isFsb():
		return fmt.Sprintf("port %d (used last time) is in use by another fsb: %s; stop it with \"fsb --stop\" (or \"kill %d\"), or run again with --port N", port, p, p.pid)
	default:
		return fmt.Sprintf("port %d (used last time) is in use by %s; run again with --port N to use a different one", port, p)
	}
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
