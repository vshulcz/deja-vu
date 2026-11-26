//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

const (
	advisoryLockExclusive       = 0x2
	advisoryLockFailImmediately = 0x1
)

var (
	advisoryLockKernel32 = syscall.NewLazyDLL("kernel32.dll")
	procAdvisoryLock     = advisoryLockKernel32.NewProc("LockFileEx")
	procAdvisoryUnlock   = advisoryLockKernel32.NewProc("UnlockFileEx")
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
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	flags := uint32(advisoryLockExclusive)
	if !wait {
		flags |= advisoryLockFailImmediately
	}
	if err := lockFileEx(h, flags, 0, 1, 0, &ol); err != nil {
		_ = f.Close()
		return func() {}, false, true
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockFileEx(h, 0, 1, 0, &ol)
			_ = f.Close()
		})
	}, true, false
}

func lockFileEx(h syscall.Handle, flags, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procAdvisoryLock.Call(uintptr(h), uintptr(flags), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol))) //nolint:gosec // LockFileEx takes the OVERLAPPED by pointer
	if r1 == 0 {
		if !errors.Is(e1, syscall.Errno(0)) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}

func unlockFileEx(h syscall.Handle, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procAdvisoryUnlock.Call(uintptr(h), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol))) //nolint:gosec // UnlockFileEx takes the OVERLAPPED by pointer
	if r1 == 0 {
		if !errors.Is(e1, syscall.Errno(0)) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}
