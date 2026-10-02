//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// holdAiderRun takes a shared hold on path for as long as one `deja aider`
// runs. done, called on the way out, runs ifLast only when no other `deja
// aider` holds one, under an exclusive hold so one starting meanwhile writes
// its digest after it. ok=false means there is no lock to be had here.
func holdAiderRun(path string) (done func(ifLast func()), ok bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		_ = f.Close()
		return nil, false
	}
	return func(ifLast func()) {
		defer func() { _ = f.Close() }()
		if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			ifLast()
		}
		_ = syscall.Flock(fd, syscall.LOCK_UN)
	}, true
}
