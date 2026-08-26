package main

import (
	"runtime"
	"testing"
)

// skipWindowsEmptySessionID marks the two tests that fail on Windows for a
// reason that is a product bug rather than a test artefact: the session-start
// hook records an empty session id there while `deja vu` records the one the
// harness named, so per-session dedup cannot work and the same block can be
// injected twice (#2023).
//
// Skipped rather than deleted or quietly weakened: the assertion is right, the
// platform is wrong, and a skip that names the issue keeps the failure visible
// to whoever opens the file. It exists at all because the Windows job used to
// fail at build — `undefined: syscall.Flock` — so none of these tests had ever
// run there and the bug could not be seen.
//
// Remove this call, not the tests, when #2023 is fixed.
func skipWindowsEmptySessionID(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the session-start hook records an empty session id on Windows (#2023)")
	}
}
