package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A bucket file cut short by a crash must read as "corrupt index" and
// SearchWithRecovery must rebuild and answer instead of erroring.
func TestSearchRecoversFromCorruptBucket(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-heal")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"h1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"healneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "h1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "healneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}

	// Truncate every bucket to simulate a crash mid-write.
	buckets, err := filepath.Glob(filepath.Join(dir, "buckets", "*.bin"))
	if err != nil || len(buckets) == 0 {
		t.Fatalf("buckets glob err=%v n=%d", err, len(buckets))
	}
	for _, b := range buckets {
		if err := os.Truncate(b, 3); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Search(dir, o); !IsCorrupt(err) {
		t.Fatalf("Search error = %v, want corrupt index", err)
	}
	ss, err := SearchWithRecovery(dir, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "h1" {
		t.Fatalf("recovery search bad result: %#v", ss)
	}
}
