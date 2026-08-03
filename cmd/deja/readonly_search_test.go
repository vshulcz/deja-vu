package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A store that cannot be written still has an index that can be read. Refusing
// the whole search then is deja withholding what it has: the reader got
// nothing instead of slightly old memory and a line saying why (#904).
func TestSearchAnswersFromAnIndexItCannotUpdate(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the winch brake pads squeal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A newer session the index does not know about yet, and a cache that
	// cannot be written to learn it.
	newer := `{"type":"user","message":{"role":"user","content":"a brand new session"},"timestamp":"2026-07-02T10:00:00Z","sessionId":"s2","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s2.jsonl"), []byte(newer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	out, err := captureRun(t, "search", "winch brake pads")
	if err != nil {
		t.Fatalf("search refused to answer from the index it has: %v", err)
	}
	if !strings.Contains(out, "the winch brake pads squeal") {
		t.Errorf("the old index was not read:\n%s", out)
	}

	// The seam itself: an index that is there and a store that is not
	// writable is the case that carries on; nothing to read is not.
	if !staleReadOnlyIndex(dir, fs.ErrPermission) {
		t.Error("a readable index with an unwritable store was treated as fatal")
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if index.HasManifest(dir) {
		t.Fatal("the index did not go away")
	}
	if staleReadOnlyIndex(dir, fs.ErrPermission) {
		t.Error("with no index at all deja still claimed it could answer")
	}
}
