package connowner

import "net/netip"

// Lookup returns the user owning the client end of a TCP connection to server
// on this machine. It is nil where the platform cannot tell (set on Linux).
var Lookup func(server, client netip.AddrPort) (int, error)
