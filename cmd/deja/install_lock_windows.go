//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	installLockExclusive       = 0x2
	installLockFailImmediately = 0x1
)

var (
	installLockKernel32 = syscall.NewLazyDLL("kernel32.dll")
	procInstallLock     = installLockKernel32.NewProc("LockFileEx")
	procInstallUnlock   = installLockKernel32.NewProc("UnlockFileEx")
)

// takeInstallLock is the windows half of the install lock; see the unix file
// for what it is for. LockFileEx blocks without the FAIL_IMMEDIATELY flag,
// which is the `wait` case here.
func takeInstallLock(path string, wait bool) (release func(), ok, busy bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() {}, false, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false, false
	}
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	flags := uint32(installLockExclusive)
	if !wait {
		flags |= installLockFailImmediately
	}
	if err := installLockFileEx(h, flags, 0, 1, 0, &ol); err != nil {
		_ = f.Close()
		return func() {}, false, true
	}
	return func() {
		_ = installUnlockFileEx(h, 0, 1, 0, &ol)
		_ = f.Close()
	}, true, false
}

func installLockFileEx(h syscall.Handle, flags, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procInstallLock.Call(uintptr(h), uintptr(flags), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}

func installUnlockFileEx(h syscall.Handle, reserved, low, high uint32, ol *syscall.Overlapped) error {
	r1, _, e1 := procInstallUnlock.Call(uintptr(h), uintptr(reserved), uintptr(low), uintptr(high), uintptr(unsafe.Pointer(ol)))
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}
