package index

import (
	"os"
	"path/filepath"
	"testing"
)

// HarnessSharedCounts is what lets doctor tell a parse failure from an id
// collision (#1101); it had no test of its own, and the package floor noticed.
func TestHarnessSharedCounts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	store := t.TempDir()
	// Two transcripts writing one id: the manifest keeps a single row for them
	// and marks it shared.
	for i, name := range []string{"a.jsonl", "b.jsonl"} {
		rec := `{"type":"user","message":{"role":"user","content":"conversation ` + name + `"},"timestamp":"2026-08-0` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"dup1","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", store)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	shared := HarnessSharedCounts(dir)
	if shared["claude"] != 1 {
		t.Errorf("shared rows for claude = %d, want 1 (two transcripts, one id)", shared["claude"])
	}
	if n, err := SessionCount(dir); err != nil || n != 1 {
		t.Errorf("session count = %d (err %v), want 1", n, err)
	}
	if counts := HarnessSessionCounts(dir); counts["claude"] != 1 {
		t.Errorf("per-harness count = %d, want 1", counts["claude"])
	}
	if imported := ImportedSessionCounts(dir); imported["claude"] != 0 {
		t.Errorf("nothing was imported, got %d", imported["claude"])
	}
	// A directory with no manifest answers empty rather than panicking.
	if got := HarnessSharedCounts(filepath.Join(t.TempDir(), "none")); len(got) != 0 {
		t.Errorf("a missing manifest returned %v", got)
	}
}
