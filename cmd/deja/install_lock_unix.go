//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// takeInstallLock holds an advisory lock on path. ok=false means there is no
// lock — the directory could not be created, or `wait` was false and someone
// else holds it — and the caller carries on without one rather than failing:
// an install that cannot take a lock is still better than no install.
//
// Advisory rather than a lockfile with a pid in it, because the kernel drops
// it when the process goes, however it goes.
// busy tells the two failures apart: somebody else holds the lock, rather than
// this machine not letting deja have one at all.
func takeInstallLock(path string, wait bool) (release func(), ok, busy bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() {}, false, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, false
}
