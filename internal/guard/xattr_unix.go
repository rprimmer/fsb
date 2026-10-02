//go:build darwin || linux

package guard

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// listXattr returns the attribute names of an open file. A filesystem without
// extended-attribute support yields no names, not an error.
func listXattr(fd int) ([]string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		sz, err := unix.Flistxattr(fd, nil)
		if err != nil {
			return nil, ignoreUnsupported(err)
		}
		if sz == 0 {
			return nil, nil
		}
		buf := make([]byte, sz)
		n, err := unix.Flistxattr(fd, buf)
		if errors.Is(err, unix.ERANGE) {
			continue // the list grew between the two calls
		}
		if err != nil {
			return nil, ignoreUnsupported(err)
		}
		var names []string
		for _, name := range strings.Split(string(buf[:n]), "\x00") {
			if name != "" {
				names = append(names, name)
			}
		}
		return names, nil
	}
	return nil, nil
}

// fgetxattr is unix.Fgetxattr; a variable so a test can make an attribute
// change between the two calls getXattr makes.
var fgetxattr = unix.Fgetxattr

var errXattrChanging = errors.New("extended attribute kept changing while it was read")

// getXattr reads one attribute. If it is larger than max, only its size is
// returned (value is nil). The attribute is another program's to change at any
// moment, so a size that differs between asking and reading is asked again,
// a few times, rather than trusted.
func getXattr(fd int, name string, max int) (value []byte, size int, err error) {
	for attempt := 0; attempt < 3; attempt++ {
		size, err = fgetxattr(fd, name, nil)
		if err != nil {
			return nil, 0, ignoreUnsupported(err)
		}
		if size > max {
			return nil, size, nil
		}
		if size == 0 {
			// Empty when asked. A read into no room is only another size query
			// (it could answer a size it has no space for), so there is nothing
			// to read: the observed value is the empty one.
			return []byte{}, 0, nil
		}
		buf := make([]byte, size)
		n, err := fgetxattr(fd, name, buf)
		if errors.Is(err, unix.ERANGE) || n > len(buf) {
			continue // it grew between the two calls
		}
		if err != nil {
			return nil, size, ignoreUnsupported(err)
		}
		return buf[:n], n, nil
	}
	return nil, size, errXattrChanging
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, errNoAttr) {
		return nil
	}
	return err
}
