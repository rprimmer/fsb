package connowner

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
)

// connPair opens a real loopback connection and returns both ends.
func connPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err = ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return client, server
}

// socketInode is the inode of conn's socket, as /proc/self/fd names it.
func socketInode(t *testing.T, conn net.Conn) uint64 {
	t.Helper()
	f, err := conn.(*net.TCPConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
	if err != nil {
		t.Fatalf("%s: %v", link, err)
	}
	return n
}

// procConnOwner finds the client end of a real connection: its owner (this
// process), and, since both ends belong to the same user here, its inode, to
// show it is the client's row and not fsb's own end of the same connection
// (which would let any user through).
func TestProcConnOwnerFindsTheClientEnd(t *testing.T) {
	c, s := connPair(t)
	server := netip.MustParseAddrPort(s.LocalAddr().String())
	client := netip.MustParseAddrPort(s.RemoteAddr().String())
	r, err := procConnRow(server, client)
	if err != nil || r.uid != os.Geteuid() {
		t.Fatalf("procConnRow = %+v, %v; want uid %d", r, err, os.Geteuid())
	}
	if want, own := socketInode(t, c), socketInode(t, s); r.inode != want {
		t.Fatalf("matched socket %d; the client's is %d (fsb's own end: %d)", r.inode, want, own)
	}
	if uid, err := procConnOwner(server, client); err != nil || uid != os.Geteuid() {
		t.Errorf("procConnOwner = %d, %v", uid, err)
	}
	// A connection that does not exist has no owner.
	gone := netip.AddrPortFrom(client.Addr(), client.Port()+1)
	if uid, err := procConnOwner(server, gone); err == nil {
		t.Errorf("a connection that does not exist was given owner %d", uid)
	}
}

// A client that closes its sending side after its request (as `nc -N` does)
// is still found: its end is then in FIN_WAIT2.
func TestProcConnOwnerFindsAHalfClosedClient(t *testing.T) {
	c, s := connPair(t)
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	s.Read(buf) // the FIN arrives: the client's end is now in FIN_WAIT2
	server := netip.MustParseAddrPort(s.LocalAddr().String())
	client := netip.MustParseAddrPort(s.RemoteAddr().String())
	if r, err := procConnRow(server, client); err != nil || r.inode != socketInode(t, c) {
		t.Fatalf("procConnRow = %+v, %v; want the half-closed client's socket", r, err)
	}
}

// The tables list both ends of a loopback connection; only the client's end
// (its own address is the client's, its peer the server's) names the client's
// owner. Listening sockets and TIME_WAIT are not it.
func TestFindOwnerReadsTheClientsRow(t *testing.T) {
	// procAddr decodes in the machine's byte order, so build the rows the
	// same way (procHex); TestProcAddrByteOrder pins the order itself.
	ip4 := procHex([]byte{127, 0, 0, 1})
	ip6mapped := procHex(netip.MustParseAddr("::ffff:127.0.0.1").AsSlice())
	server := netip.MustParseAddrPort("127.0.0.1:4242")
	client := netip.MustParseAddrPort("127.0.0.1:50000")
	tcp := strings.Join([]string{head,
		tcpRow(ip4, "00000000", 4242, 0, "0A", 1000, 1),     // fsb listening
		tcpRow(ip4, ip4, 4242, 50000, "01", 1000, 2),        // fsb's end of the connection
		tcpRow(ip4, ip4, 50000, 4242, "06", 0, 3),           // an old connection in TIME_WAIT
		tcpRow(ip4, ip4, 50001, 4242, "01", 1003, 4)}, "\n") // another client
	if r, err := findOwner([][]byte{[]byte(tcp)}, server, client, 65534); err == nil {
		t.Errorf("found %+v for a client with no row of its own", r)
	}
	withClient := tcp + "\n" + tcpRow(ip4, ip4, 50000, 4242, "01", 1001, 5) + "\n"
	if r, err := findOwner([][]byte{[]byte(withClient)}, server, client, 65534); err != nil || r.uid != 1001 || r.inode != 5 {
		t.Errorf("findOwner = %+v, %v; want the client's row (uid 1001, inode 5)", r, err)
	}
	// A client using an IPv6 socket appears in tcp6 with a v4-mapped address.
	tcp6 := head + "\n" + tcpRow(ip6mapped, ip6mapped, 50000, 4242, "01", 1004, 6) + "\n"
	if r, err := findOwner([][]byte{[]byte(tcp), []byte(tcp6)}, server, client, 65534); err != nil || r.uid != 1004 {
		t.Errorf("findOwner over tcp6 = %+v, %v; want uid 1004", r, err)
	}
	// A half-closed client (FIN_WAIT1, FIN_WAIT2) is still the client's end.
	for _, st := range []string{"04", "05"} {
		half := head + "\n" + tcpRow(ip4, ip4, 50000, 4242, st, 1005, 7) + "\n"
		if r, err := findOwner([][]byte{[]byte(half)}, server, client, 65534); err != nil || r.uid != 1005 {
			t.Errorf("state %s: findOwner = %+v, %v; want uid 1005", st, r, err)
		}
	}
}

// When fsb runs in a user namespace (unshare -U, some sandboxes), users it
// cannot name all show as the overflow uid, which would match fsb's own if
// fsb is that uid. It tells nothing, so it is refused as unknown.
func TestFindOwnerRefusesTheOverflowUID(t *testing.T) {
	ip4 := procHex([]byte{127, 0, 0, 1})
	tcp := head + "\n" + tcpRow(ip4, ip4, 50000, 4242, "01", 65534, 1) + "\n"
	server := netip.MustParseAddrPort("127.0.0.1:4242")
	client := netip.MustParseAddrPort("127.0.0.1:50000")
	if r, err := findOwner([][]byte{[]byte(tcp)}, server, client, 65534); err == nil {
		t.Errorf("the overflow uid was taken for an owner: %+v", r)
	}
}

// The table's byte order, written out rather than computed: 127.0.0.1 is
// 0100007F on a little-endian machine and 7F000001 on a big-endian one.
func TestProcAddrByteOrder(t *testing.T) {
	want := netip.MustParseAddrPort("127.0.0.1:4242")
	literal := "0100007F:1092"
	if binary.NativeEndian.Uint16([]byte{0, 1}) == 1 { // big-endian
		literal = "7F000001:1092"
	}
	if got, ok := procAddr(literal); !ok || got != want {
		t.Errorf("procAddr(%q) = %v, %v; want %v", literal, got, ok, want)
	}
}

const head = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"

func tcpRow(own, peer string, ownPort, peerPort int, state string, uid int, inode uint64) string {
	return fmt.Sprintf("  0: %s:%04X %s:%04X %s 00000000:00000000 00:00000000 00000000 %5d 0 %d 1 0000000000000000 20 4 30 10 -1",
		own, ownPort, peer, peerPort, state, uid, inode)
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
