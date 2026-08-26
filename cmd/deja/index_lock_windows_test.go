//go:build windows

package main

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

const lockfileExclusiveLock = 0x2

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// holdIndexLock takes the index lock the way another deja process would, so a
// tool that waits for it waits for this test instead.
//
// The Windows half of the pair, taking the same lock internal/index takes here
// — one byte through LockFileEx — so the tests that need a held lock run on
// this platform rather than being tagged out of it (#2079).
func holdIndexLock(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	if r1, _, e1 := procLockFileEx.Call(uintptr(h), uintptr(lockfileExclusiveLock), 0, 1, 0, uintptr(unsafe.Pointer(&ol))); r1 == 0 {
		_ = f.Close()
		t.Fatalf("lock %s: %v", dir+".lock", e1)
	}
	// The same Overlapped the lock was taken with, as lockDir does: the offset
	// it carries is what names the byte range being released.
	return func() {
		_, _, _ = procUnlockFileEx.Call(uintptr(h), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
		_ = f.Close()
	}
}
