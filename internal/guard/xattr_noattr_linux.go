package guard

import "golang.org/x/sys/unix"

// errNoAttr is what getxattr returns for a missing attribute (Linux has no
// ENOATTR; it uses ENODATA).
var errNoAttr = unix.ENODATA
