package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A home whose cache directory refuses writes is a locked-down laptop, not an
// ejected disk and not a rebuild in flight — and deja told both of those
// stories, sending the reader to reconnect a disk that is present and to wait
// for a rebuild that will never finish (#2267).
func TestAnUnwritableCacheIsNotAnUnmountedDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny creation the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes through the permission")
	}
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache")
	if err := os.MkdirAll(cache, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	dir := filepath.Join(cache, "deja", "index.db")

	// The premise: the build really is refused here.
	err := index.Ensure(dir, "", false, nil)
	if err == nil {
		t.Fatal("the build succeeded under an unwritable cache")
	}

	said := ensureError(dir, err).Error()
	if strings.Contains(said, "unmounted") {
		t.Errorf("an unwritable directory was reported as an unmounted disk: %s", said)
	}
	if !strings.Contains(said, "permission") && !strings.Contains(said, "writable") {
		t.Errorf("the refusal does not name what is wrong: %s", said)
	}

	// And nothing is being rebuilt: the lock could not be created, which is not
	// the same as somebody holding it.
	if index.RebuildInProgress(dir) {
		t.Error("an unwritable index directory reads as a rebuild in progress")
	}
}
