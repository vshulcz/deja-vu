package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The export watermark is saved into the index, so an index that cannot be
// written fails the same call — and was reported against the destination,
// which was writable and already held the batch (#1046).
func TestSyncExportNamesTheIndexWhenTheIndexIsWhatIsDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Block the one file the manifest is written through, leaving the index
	// directory and the destination as writable as they were.
	idx := os.Getenv("DEJA_INDEX_DIR")
	blocked := filepath.Join(idx, "manifest.gob.tmp")
	if err := os.WriteFile(blocked, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(blocked) })

	out := filepath.Join(tmp, "export")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := captureRun(t, "sync", "export", "--full", out)
	if err == nil {
		t.Fatal("an export with an unwritable index succeeded")
	}
	got := err.Error()
	if strings.Contains(got, "cannot write the export into "+out) {
		t.Errorf("blames the destination, which is writable: %q", got)
	}
	if !strings.Contains(got, "cannot write the index at "+idx) {
		t.Errorf("does not name the index: %q", got)
	}
	// The batch is on disk; saying nothing about it reads as "nothing left".
	entries, rerr := os.ReadDir(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was written into the destination, so the message is about the wrong half")
	}
	if !strings.Contains(got, "are written into "+out) {
		t.Errorf("does not say the batch is already there: %q", got)
	}
}
