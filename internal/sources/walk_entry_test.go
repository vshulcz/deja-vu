package sources

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A PRoot directory entry can report a symlink type for a regular file,
// while its Info result still has the underlying regular mode.
type misreportedWalkEntry struct{ os.DirEntry }

func (misreportedWalkEntry) Type() fs.FileMode { return fs.ModeSymlink }

func TestWalkEntryAcceptsAMisreportedRegularFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.v4.jsonl.zstd"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !isRegularWalkEntry(misreportedWalkEntry{entries[0]}) {
		t.Fatal("a reported symlink with regular file information should be discovered")
	}
}
