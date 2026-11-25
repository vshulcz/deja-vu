//go:build !windows

package atomicfile

import (
	"os"
	"sync"
	"syscall"
)

// TakeAdvisoryLock acquires an exclusive advisory file lock on path without
// creating parent directories. When ok is false and busy is true, another
// handle holds the lock; when ok is false and busy is false, the lock file
// could not be opened. The returned release function is always safe to call
// multiple times.
func TakeAdvisoryLock(path string, wait bool) (release func(), ok, busy bool) {
	f, err := OpenSharedFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false, false
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		return func() {}, false, true
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		})
	}, true, false
}
