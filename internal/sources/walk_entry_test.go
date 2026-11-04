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
	target := filepath.Join(dir, "session.v4.jsonl.zstd")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !isRegularWalkEntry(misreportedWalkEntry{entries[0]}) {
		t.Fatal("a reported symlink with regular file information should be discovered")
	}

	linkDir := t.TempDir()
	err = os.Symlink(target, filepath.Join(linkDir, "link.jsonl"))
	if err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	linkEntries, err := os.ReadDir(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	if isRegularWalkEntry(linkEntries[0]) {
		t.Fatal("a real symlink to a regular file must stay excluded")
	}
}
