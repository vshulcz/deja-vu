//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

// holdIndexLock takes the index lock the way another deja process would, so a
// tool that waits for it waits for this test instead.
//
// Split by platform for the same reason internal/index splits lockDir: flock is
// Unix's and LockFileEx is Windows's. Keeping only the first in a plain _test.go
// file stopped cmd/deja compiling on Windows at all, so the leg that runs there
// failed before it reached a single test (#2079).
func holdIndexLock(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
