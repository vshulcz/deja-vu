package index

import (
	"runtime"
	"testing"
)

// skipWindowsPortability defers a test that bakes in unix path fixtures
// (unix-style cwd, flat store layouts) and asserts unix-derived projects,
// attribution or error wording. deja derives those from file paths with
// filepath.Rel/Separator and a lexicographic path comparison, so they diverge
// on windows. These pass on macOS and ubuntu; the windows portability of the
// fixtures — and one unproven session-count doubling — is tracked in #1119.
func skipWindowsPortability(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-path fixture; windows portability tracked in #1119")
	}
}
