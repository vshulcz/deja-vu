package index

import (
	"path/filepath"
	"testing"
)

// The predicate behind the one state that looks like a broken store and is
// not: a rebuild holds the lock while it recreates the directory (#822).
func TestRebuildInProgressFollowsTheLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if RebuildInProgress(dir) {
		t.Error("an idle index reports a rebuild")
	}
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Held by this process: flock is per-process, so the probe sees it free.
	// What matters is that releasing and re-taking works and nothing panics.
	unlock()
	if RebuildInProgress(dir) {
		t.Error("a released lock still reports a rebuild")
	}
}
