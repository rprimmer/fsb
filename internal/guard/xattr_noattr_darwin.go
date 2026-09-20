package guard

import "golang.org/x/sys/unix"

// errNoAttr is what getxattr returns for a missing attribute.
var errNoAttr = unix.ENOATTR
