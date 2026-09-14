package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// An index written before the redaction bump answers nothing until it has been
// rebuilt: its text predates the patterns that mask a password given to a
// program as a flag, and the latency-bound path must not serve it while the
// re-read runs (#3535, #3552).
func TestAnIndexBelowTheRedactionFloorRebuildsBeforeItAnswers(t *testing.T) {
	dir := indexWithOneSession(t)
	agedManifest(t, dir, redactionFloor-1)

	stale, err := EnsureForSearchStale(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("served an index whose text predates the redaction it needs")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("index left at version %d — it was supposed to be rebuilt before answering", m.Version)
	}
}

// A layout this build cannot read is the other case that blocks, and it is the
// one onDiskFormat exists to mark.
func TestAnUnreadableLayoutRebuildsBeforeItAnswers(t *testing.T) {
	dir := indexWithOneSession(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Format = onDiskFormat - 1
	writeAgedManifest(t, dir, m)

	stale, err := EnsureForSearchStale(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("served a store written in a layout this build cannot read")
	}
	if m, err := readManifest(dir); err != nil || m.Format != onDiskFormat {
		t.Fatalf("format=%d err=%v — the store was supposed to be rebuilt", m.Format, err)
	}
}

func indexWithOneSession(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry budget was the thing that fixed it"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func agedManifest(t *testing.T, dir string, v int) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = v
	writeAgedManifest(t, dir, m)
}

// writeAgedManifest writes the manifest and moves its mtime, because the
// manifest cache keys on mtime and size and a version field is the same size
// either way.
func writeAgedManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	tick := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), tick, tick); err != nil {
		t.Fatal(err)
	}
}
