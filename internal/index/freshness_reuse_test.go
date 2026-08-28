package index

import (
	"path/filepath"
	"testing"
)

// Deriving SafeSize and PrefixHash means reading each transcript — the tail
// for the last complete line, the head for the hash. On a real store that was
// 650 ms on every command, against 13 ms of actual searching, to conclude
// nothing had moved.
func TestUnchangedFilesKeepTheirDerivedState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", dir)
	path := filepath.Join(dir, "project", "session.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))

	first := currentFilesWith("", nil)
	fs, ok := first[path]
	if !ok || fs.SafeSize == 0 || fs.PrefixSample == 0 {
		t.Fatalf("derived state missing on a first walk: %+v", fs)
	}

	// Unchanged file: the values must come back identical without being
	// recomputed, which is what makes the walk a stat pass.
	second := currentFilesWith("", first)
	if second[path].SafeSize != fs.SafeSize || second[path].PrefixSample != fs.PrefixSample {
		t.Fatalf("carried state differs: %+v vs %+v", second[path], fs)
	}

	// Changed file: stale values must not be carried, or a rewind goes
	// unnoticed and the index keeps the old text.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "rewritten"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	third := currentFilesWith("", second)
	if third[path].PrefixHash == fs.PrefixHash && third[path].SafeSize == fs.SafeSize {
		t.Fatal("a rewritten file kept the old derived state")
	}
}
