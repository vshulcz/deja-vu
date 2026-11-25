//go:build !windows

package main

import "github.com/vshulcz/deja-vu/internal/atomicfile"

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
	return atomicfile.TakeAdvisoryLock(path, wait)
}
