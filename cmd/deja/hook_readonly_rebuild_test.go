package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An index this build cannot read, in a directory it cannot write. The line
// for that state hung off warmupJustRequested, whose sentinel lives inside the
// very directory that is read-only — so the hook went out empty on every
// session and nothing ever said why (#1048).
func TestHookNamesAnUnwritableIndexDirWithoutTheSentinel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory anyway")
	}
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	parent := filepath.Dir(dir)
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
	// An index a newer build left behind: readable, and unreadable by this one.
	writeStaleFormatIndex(t, dir)

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	// t.TempDir removes the tree afterwards and cannot do it through 0500.
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o700)
		_ = os.Chmod(dir, 0o700)
	})

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("hook said nothing at all on an index that needs rebuilding in a read-only directory")
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "is not writable") {
		t.Errorf("hook did not name the unwritable directory: %q", resp.SystemMessage)
	}

	// A healthy index in a writable directory keeps its silence: the notice
	// must not become something every session start carries.
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "is not writable") {
		t.Errorf("a healthy index was reported as unwritable: %q", out)
	}
}
