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

// getXattr reads one attribute. If it is larger than max, only its size is
// returned (value is nil).
func getXattr(fd int, name string, max int) (value []byte, size int, err error) {
	size, err = unix.Fgetxattr(fd, name, nil)
	if err != nil {
		return nil, 0, ignoreUnsupported(err)
	}
	if size > max {
		return nil, size, nil
	}
	buf := make([]byte, size)
	n, err := unix.Fgetxattr(fd, name, buf)
	if err != nil {
		return nil, size, ignoreUnsupported(err)
	}
	return buf[:n], n, nil
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, errNoAttr) {
		return nil
	}
	return err
}
