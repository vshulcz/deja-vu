package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Agents rewind a session by truncating the transcript and writing it again.
// Once it grows past its old length that is indistinguishable from an append
// by size alone, and appending onto it leaves the rewritten prefix in the
// index with its old text while the new text is never read.
func TestRewrittenPrefixIsNotTreatedAsAppend(t *testing.T) {
	dir := t.TempDir()
	// The append fast path only applies to recognised harness files, so the
	// fixture has to live where the claude parser looks.
	t.Setenv("DEJA_CLAUDE_ROOT", dir)
	path := filepath.Join(dir, "project", "session.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "ORIGINAL text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	before := currentFileStateFor(t, path)

	// The rewind: shorter, then longer again with a different first line.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	after := currentFileStateFor(t, path)

	if after.Size <= before.Size {
		t.Fatalf("test does not exercise the case: sizes %d -> %d", before.Size, after.Size)
	}
	if canAppendIncremental(map[string]FileState{path: after}, map[string]FileState{path: before}) {
		t.Fatal("a rewritten prefix was accepted as an append; the old text would stay indexed")
	}

	// A genuine append must still take the fast path, or every session write
	// becomes a full reparse.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN text"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"), claudeLine("s1", "2026-01-01T00:03:00Z", "third"), claudeLine("s1", "2026-01-01T00:04:00Z", "fourth"))
	grown := currentFileStateFor(t, path)
	if !canAppendIncremental(map[string]FileState{path: grown}, map[string]FileState{path: after}) {
		t.Fatal("a plain append no longer takes the append path")
	}
}

func currentFileStateFor(t *testing.T, path string) FileState {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fs := FileState{Path: path, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	fs.SafeSize = lastCompleteLineOffset(path, fi.Size())
	fs.PrefixHash = filePrefixHash(path, fs.SafeSize)
	return fs
}
