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
// It lives behind a build tag because syscall.Flock does not exist on Windows:
// with the helper in the shared file, `go vet` on windows-latest failed to
// build the package at all — "undefined: syscall.Flock" — which took the whole
// Windows job red rather than one test.
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
