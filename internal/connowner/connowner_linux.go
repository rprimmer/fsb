// Package connowner tells which user owns the client end of a TCP connection
// to fsb, where the platform can tell (Linux). It is the one part of the
// launch check that reads the file system, so it stays outside the HTTP
// packages, which may not.
package connowner

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func init() { Lookup = procConnOwner }

// procConnOwner returns the user owning the client end of a TCP connection to
// server on this machine, from /proc/net/tcp and tcp6. Their uid column is
// given in fsb's own user namespace, so a browser in a sandbox (Flatpak, Snap)
// shows as the person's user.
func procConnOwner(server, client netip.AddrPort) (int, error) {
	r, err := procConnRow(server, client)
	return r.uid, err
}

// row is the client end of a connection, as /proc/net/tcp lists it.
type row struct {
	uid   int
	inode uint64
}

func procConnRow(server, client netip.AddrPort) (row, error) {
	var tables [][]byte
	var readErr error
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			readErr = err
			continue
		}
		tables = append(tables, b)
	}
	if len(tables) == 0 {
		return row{}, fmt.Errorf("cannot read the connection table: %w", readErr)
	}
	return findOwner(tables, server, client, overflowUID())
}

// overflowUID is the uid the kernel shows for a user that fsb's user
// namespace cannot name. Every such user looks alike, so it says nothing.
func overflowUID() int {
	if b, err := os.ReadFile("/proc/sys/kernel/overflowuid"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			return n
		}
	}
	return 65534
}

// The states in which a socket is the client end of a connection whose
// request fsb may be answering: ESTABLISHED, and FIN_WAIT1 and FIN_WAIT2 for a
// client that closed its sending side after its request. Not TIME_WAIT,
// whose rows no longer carry an owner.
var clientStates = map[string]bool{"01": true, "04": true, "05": true}

// findOwner returns the row of the connection whose own address is client
// and whose peer is server: the client's end. fsb's own end lists the same
// two addresses the other way round.
func findOwner(tables [][]byte, server, client netip.AddrPort, overflow int) (row, error) {
	for _, t := range tables {
		lines := strings.Split(string(t), "\n")
		for _, l := range lines[1:] { // after the heading
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			f := strings.Fields(l)
			if len(f) < 10 || !clientStates[f[3]] {
				continue
			}
			own, ok1 := procAddr(f[1])
			peer, ok2 := procAddr(f[2])
			if !ok1 || !ok2 || own != client || peer != server {
				continue
			}
			uid, err1 := strconv.Atoi(f[7])
			inode, err2 := strconv.ParseUint(f[9], 10, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			if uid == overflow {
				return row{}, errors.New("the connection's owner is not visible from fsb's user namespace")
			}
			return row{uid: uid, inode: inode}, nil
		}
	}
	return row{}, errors.New("the connection is not listed in /proc/net/tcp")
}

// procAddr decodes an address:port from /proc/net/tcp*: the address's bytes
// printed as hexadecimal 32-bit words in the machine's own byte order, and the
// port in hexadecimal. A v4-mapped IPv6 address is returned as IPv4.
func procAddr(s string) (netip.AddrPort, bool) {
	h, p, ok := strings.Cut(s, ":")
	raw, err := hex.DecodeString(h)
	port, err2 := strconv.ParseUint(p, 16, 16)
	if !ok || err != nil || err2 != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(b[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	a, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(a.Unmap(), uint16(port)), true
}
