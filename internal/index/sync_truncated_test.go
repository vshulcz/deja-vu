package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A batch whose last line was cut off mid-write is a transfer that did not all
// arrive, not a foreign record. The whole file is still refused (#891), but the
// reason must say "truncated, fetch it again" rather than "not a record deja
// wrote", which reads as a corrupt or hostile batch (#1117).
func TestImportNamesATruncatedBatch(t *testing.T) {
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
	good := `{"harness":"claude","session_id":"r1","project":"p","role":"user","text":"first","time":"2026-07-20T10:00:00Z"}` + "\n"
	torn := `{"harness":"claude","session_id":"r2","project":"p","role":"user","text":"cut off mid-tra`
	if err := os.WriteFile(filepath.Join(in, "deja-sync-abc-1.jsonl"), []byte(good+torn), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Import(dir, in)
	if err == nil {
		t.Fatal("a truncated batch was accepted")
	}
	if !strings.Contains(err.Error(), "truncated") || !strings.Contains(err.Error(), "fetch the batch again") {
		t.Errorf("truncation not named as such: %v", err)
	}
	if strings.Contains(err.Error(), "not a record deja wrote") {
		t.Errorf("a truncated transfer read as a foreign record: %v", err)
	}

	// A corrupt line in the middle (with lines after it) is a different thing —
	// it keeps the mistrustful wording.
	mid := good + `{bad}` + "\n" + good
	if err := os.WriteFile(filepath.Join(in, "deja-sync-abc-1.jsonl"), []byte(mid), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Import(dir, in)
	if err == nil || !strings.Contains(err.Error(), "not a record deja wrote") {
		t.Errorf("a corrupt middle line should stay mistrusted: %v", err)
	}
}
