package ctxcache

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	ctxKernel32     = syscall.NewLazyDLL("kernel32.dll")
	ctxLockFileEx   = ctxKernel32.NewProc("LockFileEx")
	ctxUnlockFileEx = ctxKernel32.NewProc("UnlockFileEx")
)

// Lock one byte on the stable lock file. The kernel releases the lock when the
// handle closes or its process terminates, so a crashed writer cannot leave a
// permanent marker that prevents future checkpoints.
func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	ok, _, err := ctxLockFileEx.Call(f.Fd(), 2, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		_ = f.Close()
		return nil, fmt.Errorf("lock context cache: %w", err)
	}
	return f, nil
}

func unlockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, err := ctxUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return fmt.Errorf("unlock context cache: %w", err)
	}
	return nil
}
