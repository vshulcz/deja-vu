//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// holdAiderRun is the windows half; see the unix file. LockFileEx cannot turn
// a shared lock exclusive, so done lets go of the shared one first.
func holdAiderRun(path string) (done func(ifLast func()), ok bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	if err := installLockFileEx(h, 0, 0, 1, 0, &ol); err != nil {
		_ = f.Close()
		return nil, false
	}
	return func(ifLast func()) {
		defer func() { _ = f.Close() }()
		_ = installUnlockFileEx(h, 0, 1, 0, &ol)
		var xol syscall.Overlapped
		if installLockFileEx(h, installLockExclusive|installLockFailImmediately, 0, 1, 0, &xol) == nil {
			ifLast()
			_ = installUnlockFileEx(h, 0, 1, 0, &xol)
		}
	}, true
}
