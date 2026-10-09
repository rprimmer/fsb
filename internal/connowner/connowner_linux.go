// Package connowner tells which user owns the client end of a TCP connection
// to fsb, where the platform can tell (Linux). It is the one part of the
// launch check that reads the file system, so it stays outside the HTTP
// packages, which may not.
package connowner

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func init() { Lookup = procConnOwner }

// procConnOwner returns the user owning the client end of a TCP connection
// to server on this machine, from /proc/net/tcp and tcp6. Their uid column is
// given in fsb's own user namespace, so a browser in a sandbox (Flatpak,
// Snap) shows as the person's user.
func procConnOwner(server, client netip.AddrPort) (int, error) {
	var tables [][]byte
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if b, err := os.ReadFile(f); err == nil {
			tables = append(tables, b)
		}
	}
	if uid, ok := findOwner(tables, server, client); ok {
		return uid, nil
	}
	return 0, errors.New("the connection is not listed in /proc/net/tcp")
}

// findOwner returns the uid of the established connection whose own address
// is client and whose peer is server: the client's end. fsb's own end lists
// the same two addresses the other way round.
func findOwner(tables [][]byte, server, client netip.AddrPort) (int, bool) {
	for _, t := range tables {
		lines := strings.Split(string(t), "\n")
		for _, l := range lines[1:] { // after the heading
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			f := strings.Fields(l)
			if len(f) < 8 || f[3] != "01" { // 01: ESTABLISHED
				continue
			}
			own, ok1 := procAddr(f[1])
			peer, ok2 := procAddr(f[2])
			if !ok1 || !ok2 || own != client || peer != server {
				continue
			}
			if uid, err := strconv.Atoi(f[7]); err == nil {
				return uid, true
			}
		}
	}
	return 0, false
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
