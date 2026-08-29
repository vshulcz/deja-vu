package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #1048's line hangs on `indexDirWritable`, which probed the index directory's
// parent. A read-only index directory inside a writable parent — a cache
// directory owned by another user, a read-only subtree — was therefore read as
// writable, and the session start went out silent in the one state deja never
// recovers from on its own (#2499).
func TestHookNamesAReadOnlyIndexDirInsideAWritableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory anyway")
	}
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"zeppelin gasket"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	writeStaleFormatIndex(t, dir)

	// The index directory alone. Its parent stays writable, which is the whole
	// point: that is the shape the old probe could not see.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the session start said nothing at all on an index that cannot answer and cannot be rebuilt")
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "is not writable") {
		t.Errorf("the hook did not name the directory that cannot be written: %q", resp.SystemMessage)
	}
	if !strings.Contains(resp.SystemMessage, dir) {
		t.Errorf("the hook named a directory that is not the one at fault: %q", resp.SystemMessage)
	}
}
