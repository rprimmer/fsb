//go:build darwin || linux

package guard

import (
	"testing"

	"golang.org/x/sys/unix"
)

// scripted replaces Fgetxattr with a function that answers from a list of
// (size, error) results for size queries and for reads into a buffer.
func scripted(t *testing.T, f func(dest []byte) (int, error)) {
	t.Helper()
	old := fgetxattr
	fgetxattr = func(_ int, _ string, dest []byte) (int, error) { return f(dest) }
	t.Cleanup(func() { fgetxattr = old })
}

// An attribute that is empty when its size is asked for and has grown by the
// time it is read must not panic: the second call into an empty buffer is just
// another size query, and its answer cannot be sliced out of that buffer.
func TestAnEmptyXattrThatGrowsDoesNotPanic(t *testing.T) {
	calls := 0
	scripted(t, func(dest []byte) (int, error) {
		calls++
		if calls == 1 {
			return 0, nil
		}
		return 5, nil // "grown": a size query with no room to hold it
	})
	val, size, err := getXattr(0, "user.x", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if size != len(val) {
		t.Errorf("size %d does not match the %d bytes returned", size, len(val))
	}
}

// An attribute that grows between the size query and the read is read again,
// not reported as an error and not cut short.
func TestAnXattrThatGrowsBetweenTheCallsIsReadAgain(t *testing.T) {
	sizes := []int{4, 9}
	q := 0
	scripted(t, func(dest []byte) (int, error) {
		if dest == nil {
			s := sizes[min(q, len(sizes)-1)]
			q++
			return s, nil
		}
		if len(dest) < 9 {
			return 0, unix.ERANGE
		}
		copy(dest, "123456789")
		return 9, nil
	})
	val, size, err := getXattr(0, "user.x", 4096)
	if err != nil || size != 9 || string(val) != "123456789" {
		t.Errorf("got %q, %d, %v", val, size, err)
	}
}

// One that keeps growing is given up on (an error, so the caller shows only the
// name), never allowed to loop or to over-read.
func TestAnXattrThatKeepsGrowingIsGivenUpOn(t *testing.T) {
	n := 0
	scripted(t, func(dest []byte) (int, error) {
		if dest == nil {
			n++
			return n * 10, nil
		}
		return 0, unix.ERANGE
	})
	if _, _, err := getXattr(0, "user.x", 4096); err == nil {
		t.Error("expected an error for an attribute that never settled")
	}
}

// A read that claims more than the buffer holds is not trusted.
func TestAnXattrReadLongerThanItsBufferDoesNotPanic(t *testing.T) {
	scripted(t, func(dest []byte) (int, error) {
		if dest == nil {
			return 4, nil
		}
		return 6, nil
	})
	getXattr(0, "user.x", 4096) // must not panic
}
