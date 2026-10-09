package connowner

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
)

// procConnOwner finds the owner of the client end of a real connection: this
// process, here.
func TestProcConnOwnerFindsTheClientEndsOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := netip.MustParseAddrPort(s.LocalAddr().String())
	client := netip.MustParseAddrPort(s.RemoteAddr().String())
	uid, err := procConnOwner(server, client)
	if err != nil || uid != os.Geteuid() {
		t.Fatalf("procConnOwner = %d, %v; want %d", uid, err, os.Geteuid())
	}
	// A connection that does not exist has no owner.
	gone := netip.AddrPortFrom(client.Addr(), client.Port()+1)
	if uid, err := procConnOwner(server, gone); err == nil {
		t.Errorf("a connection that does not exist was given owner %d", uid)
	}
}

// The tables list both ends of a loopback connection; only the client's end
// (its own address is the client's, its peer the server's) names the client's
// owner. Listening sockets and other states are not connections.
func TestFindOwnerReadsTheClientsRow(t *testing.T) {
	// 127.0.0.1 is printed 0100007F on a little-endian machine; procAddr
	// decodes in the machine's byte order, so build the rows the same way.
	ip4 := procHex([]byte{127, 0, 0, 1})
	ip6mapped := procHex(netip.MustParseAddr("::ffff:127.0.0.1").AsSlice())
	row := func(own, peer string, ownPort, peerPort int, state string, uid int) string {
		return fmt.Sprintf("  0: %s:%04X %s:%04X %s 00000000:00000000 00:00000000 00000000 %5d 0 1 1 0000000000000000 20 4 30 10 -1",
			own, ownPort, peer, peerPort, state, uid)
	}
	head := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"
	server := netip.MustParseAddrPort("127.0.0.1:4242")
	client := netip.MustParseAddrPort("127.0.0.1:50000")
	tcp := strings.Join([]string{head,
		row(ip4, "00000000", 4242, 0, "0A", 1000),     // fsb listening
		row(ip4, ip4, 4242, 50000, "01", 1000),        // fsb's end of the connection
		row(ip4, ip4, 50000, 4242, "06", 1002),        // an old connection in TIME_WAIT
		row(ip4, ip4, 50001, 4242, "01", 1003)}, "\n") // another client
	if uid, ok := findOwner([][]byte{[]byte(tcp)}, server, client); ok {
		t.Errorf("found owner %d for a client with no established row", uid)
	}
	withClient := tcp + "\n" + row(ip4, ip4, 50000, 4242, "01", 1001) + "\n"
	if uid, ok := findOwner([][]byte{[]byte(withClient)}, server, client); !ok || uid != 1001 {
		t.Errorf("findOwner = %d, %v; want the client's owner 1001", uid, ok)
	}
	// A client using an IPv6 socket appears in tcp6 with a v4-mapped address.
	tcp6 := head + "\n" + row(ip6mapped, ip6mapped, 50000, 4242, "01", 1004) + "\n"
	if uid, ok := findOwner([][]byte{[]byte(tcp), []byte(tcp6)}, server, client); !ok || uid != 1004 {
		t.Errorf("findOwner over tcp6 = %d, %v; want 1004", uid, ok)
	}
}

// procHex prints an address as /proc/net/tcp does: 32-bit words in the
// machine's byte order, in hexadecimal.
func procHex(b []byte) string {
	var s strings.Builder
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:i+4])
		fmt.Fprintf(&s, "%08X", binary.NativeEndian.Uint32(w[:]))
	}
	return s.String()
}
