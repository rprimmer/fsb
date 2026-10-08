//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// On Linux, processes and sockets are read from /proc rather than with ps and
// lsof: minimal systems lack them, and BusyBox's ps lacks the options needed.
// As with lsof, the sockets of another user's processes are not visible.

// clockTicks is the unit of a process's start time in /proc/PID/stat. It is
// USER_HZ, which is 100 on every architecture Go supports on Linux; ps uses
// the same figure.
const clockTicks = 100

// procStat holds the fields of /proc/PID/stat that fsb uses.
type procStat struct {
	name  string // the command name (at most 15 bytes)
	tty   string // e.g. "pts/0"; "" if none
	start uint64 // clock ticks after boot
}

// parseStat parses /proc/PID/stat. The command name is in parentheses and may
// contain anything, parentheses and spaces included, so the fields after it
// are counted from the last ")".
func parseStat(s string) (procStat, bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return procStat{}, false
	}
	f := strings.Fields(s[end+1:]) // f[0] is field 3 (state), so field n is f[n-3]
	if len(f) < 20 {
		return procStat{}, false
	}
	tty, err := strconv.ParseInt(f[4], 10, 64) // field 7, tty_nr
	if err != nil {
		return procStat{}, false
	}
	start, err := strconv.ParseUint(f[19], 10, 64) // field 22, starttime
	if err != nil {
		return procStat{}, false
	}
	return procStat{name: s[open+1 : end], tty: ttyName(uint64(uint32(tty))), start: start}, true
}

// ttyName names the terminal with device number nr as ps does, or returns ""
// for none (or one fsb has no name for).
func ttyName(nr uint64) string {
	major := (nr >> 8) & 0xfff
	minor := nr&0xff | (nr>>12)&0xfff00
	switch {
	case nr == 0:
		return ""
	case major >= 136 && major <= 143: // pseudo-terminals
		return fmt.Sprintf("pts/%d", (major-136)<<8+minor)
	case major == 4 && minor < 64:
		return fmt.Sprintf("tty%d", minor)
	case major == 4:
		return fmt.Sprintf("ttyS%d", minor-64)
	}
	return ""
}

// executablePath returns the file the process pid was started from. If that
// file has since been replaced or removed (as when fsb is rebuilt while it
// runs), Linux appends " (deleted)", which is dropped.
func executablePath(pid int) (string, bool) {
	p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(p, " (deleted)"), true
}

// lookupProcess describes the process pid, or reports that there is none.
func lookupProcess(pid int) (process, bool) {
	if pid <= 1 {
		return process{}, false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return process{}, false
	}
	st, ok := parseStat(string(data))
	if !ok {
		return process{}, false
	}
	uid, ok := effectiveUID(pid)
	if !ok {
		return process{}, false
	}
	boot, ok := bootTime()
	if !ok {
		return process{}, false
	}
	started := time.Unix(boot+int64(st.start/clockTicks), 0).Format(lstartLayout)
	return process{pid: pid, uid: uid, name: st.name, started: started, startID: fmt.Sprintf("ticks:%d", st.start), tty: st.tty}, true
}

// effectiveUID returns the effective user ID of process pid (what ps calls
// uid), from the "Uid:" line of /proc/PID/status: real, effective, saved, fs.
func effectiveUID(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "Uid:"); ok {
			f := strings.Fields(rest)
			if len(f) < 2 {
				return 0, false
			}
			uid, err := strconv.Atoi(f[1])
			return uid, err == nil
		}
	}
	return 0, false
}

// bootTime returns when the system booted, in seconds since 1970, from the
// "btime" line of /proc/stat.
func bootTime() (int64, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			t, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			return t, err == nil
		}
	}
	return 0, false
}

// listener is a listening TCP socket from /proc/net/tcp or tcp6.
type listener struct {
	ip    net.IP
	port  int
	inode uint64
}

// listeners returns the listening TCP sockets of this network namespace.
func listeners() []listener {
	var out []listener
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Scan() // the heading
		for sc.Scan() {
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			f := strings.Fields(sc.Text())
			if len(f) < 10 || f[3] != "0A" { // 0A: LISTEN
				continue
			}
			addr, portHex, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			ip, ok := procIP(addr)
			port, err1 := strconv.ParseUint(portHex, 16, 16)
			inode, err2 := strconv.ParseUint(f[9], 10, 64)
			if !ok || err1 != nil || err2 != nil {
				continue
			}
			out = append(out, listener{ip: ip, port: int(port), inode: inode})
		}
	}
	return out
}

// procIP decodes an address from /proc/net/tcp*: the address's bytes, printed
// as hexadecimal 32-bit words in the machine's own byte order.
func procIP(h string) (net.IP, bool) {
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(ip[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	return ip, true
}

// socketInodes returns the inodes of the sockets process pid has open. They
// are readable only for this user's own processes.
func socketInodes(pid int) map[uint64]bool {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	inodes := map[uint64]bool{}
	for _, e := range entries {
		target, err := os.Readlink(dir + "/" + e.Name())
		if err != nil {
			continue
		}
		if s, ok := strings.CutPrefix(target, "socket:["); ok {
			if n, err := strconv.ParseUint(strings.TrimSuffix(s, "]"), 10, 64); err == nil {
				inodes[n] = true
			}
		}
	}
	return inodes
}

// portHolder returns the process listening on port (on any address), if one
// of this user's processes is.
func portHolder(port int) (process, bool) {
	want := map[uint64]bool{}
	for _, l := range listeners() {
		if l.port == port {
			want[l.inode] = true
		}
	}
	if len(want) == 0 {
		return process{}, false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return process{}, false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		for inode := range socketInodes(pid) {
			if want[inode] {
				return lookupProcess(pid)
			}
		}
	}
	return process{}, false
}

// listeningPort returns the port process pid listens on at 127.0.0.1, if it
// is one of this user's processes.
func listeningPort(pid int) (int, bool) {
	inodes := socketInodes(pid)
	for _, l := range listeners() {
		if inodes[l.inode] && l.ip.Equal(net.IPv4(127, 0, 0, 1)) {
			return l.port, true
		}
	}
	return 0, false
}
