package peers

import (
	"errors"
	"io/fs"
	"runtime"
	"syscall"
	"testing"
)

// A refusal that means somebody else holds the lock is waited out; one that
// means deja cannot have it at all falls through to the write, which is what
// keeps a sync from failing for want of a lock (#1884, #2808).
func TestALockRefusalIsReadForWhatItMeans(t *testing.T) {
	windows := runtime.GOOS == "windows"
	for _, c := range []struct {
		name string
		err  error
		held bool
	}{
		{"a sharing violation", syscall.Errno(32), windows},
		{"access denied", syscall.Errno(5), windows},
		{"a lock violation", syscall.Errno(33), windows},
		{"a refusal that is not one of those", syscall.Errno(13), false},
		{"no such file", fs.ErrNotExist, false},
		{"something with no errno in it", errors.New("the volume was dismounted"), false},
	} {
		if got := lockHeldElsewhere(c.err); got != c.held {
			t.Errorf("%s: lockHeldElsewhere = %v, want %v", c.name, got, c.held)
		}
	}
}
