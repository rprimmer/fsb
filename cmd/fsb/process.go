package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// lstartLayout is how ps prints a start time (its "lstart" field).
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// process describes a running program, as far as fsb's messages need it.
type process struct {
	pid     int
	uid     int
	name    string // command name as ps shows it, without its folder
	started string // as ps prints it, e.g. "Fri Oct  2 06:08:27 2026" (lstartLayout), for messages
	// startID identifies the start time independently of the time zone (and, on
	// Linux, of changes to the clock): "ticks:N" since boot on Linux, "unix:N"
	// seconds elsewhere.  It is what the PID file records.
	startID string
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
