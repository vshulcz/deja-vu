//go:build windows

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const lockfileExclusiveLock = 0x2

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func lockDir(dir string) (func(), error) {
	lockPath := dir + ".lock"
	// Tighten pre-existing indexes created before the 0700 default.
	_ = os.Chmod(dir, 0o700)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	if err := lockFileEx(h, lockfileExclusiveLock, 0, 1, 0, &ol); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock index: %w", err)
	}
	return func() {
		_ = unlockFileEx(h, 0, 1, 0, &ol)
		_ = f.Close()
	}, nil
}

func lockFileEx(h syscall.Handle, flags, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procLockFileEx.Call(uintptr(h), uintptr(flags), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}

func unlockFileEx(h syscall.Handle, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procUnlockFileEx.Call(uintptr(h), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}
