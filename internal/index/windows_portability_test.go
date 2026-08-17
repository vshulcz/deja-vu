package index

import (
	"runtime"
	"testing"
)

// skipWindowsPortability is kept as the single place a windows-only skip would
// go, and is deliberately unused: the skips it held are removed, so windows CI
// reports what actually fails rather than reporting nothing. See #1119.
//
//nolint:unused
func skipWindowsPortability(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-path fixture; windows portability tracked in #1119")
	}
}
