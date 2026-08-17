package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// An empty postings directory is the same loss as a missing one — what
// `rm buckets/*.bin` or a copy that skipped the contents leaves behind. Every
// check passed, doctor said "built, up to date", and search answered "no
// matches in N indexed sessions" about text still in the record log (#946).
func TestAnEmptyBucketDirectoryCountsAsDamage(t *testing.T) {
	tmp := t.TempDir()
	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Every root the build consults, or another package's store leaks in and
	// this index is not the one the test wrote.
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a fresh index was called damaged")
	}

	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no bucket files to remove")
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, "buckets", e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if !Damaged(dir) {
		t.Error("an index with no postings at all was called intact")
	}

	// And the next search rebuilds rather than reporting the history empty.
	ss, err := SearchWithRecovery(dir, query.Options{Query: "ticker window"}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("search still answers nothing after the recovery path ran")
	}
}
