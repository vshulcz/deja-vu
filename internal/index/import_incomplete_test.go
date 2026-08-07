package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A record with no session_id cannot be attributed and is dropped. Dropping it
// silently made "imported 2 records" from a 3-record batch read as a complete
// transfer; the count is surfaced so a partial import is visible (#1118).
func TestImportCountsRecordsItCannotAttribute(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.MkdirAll(filepath.Join(tmp, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	batch := `{"harness":"claude","session_id":"g1","project":"p","role":"user","text":"one","time":"2026-07-20T10:00:00Z"}` + "\n" +
		`{"harness":"claude","project":"p","role":"user","text":"no session id","time":"2026-07-20T10:01:00Z"}` + "\n" +
		`{"harness":"claude","session_id":"g3","project":"p","role":"user","text":"three","time":"2026-07-20T10:02:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(in, "deja-sync-x-1.jsonl"), []byte(batch), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("imported = %d, want 2", n)
	}
	if got := ImportSkippedIncomplete(); got != 1 {
		t.Errorf("ImportSkippedIncomplete = %d, want 1", got)
	}
}
