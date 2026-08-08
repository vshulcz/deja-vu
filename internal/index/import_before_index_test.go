package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Importing into a dir that was never indexed writes the initial manifest.
// That manifest used to snapshot every local transcript as already seen while
// ingesting none, so the next index found itself fresh and skipped the
// reader's own sessions — local history stayed invisible until a full rebuild.
// A local session and the imported records must both be reachable after a
// plain import-then-index.
func TestImportBeforeFirstIndexKeepsLocalSessions(t *testing.T) {
	skipWindowsPortability(t)
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-local")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"localneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "local1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")

	// Import first, into a dir that has never been indexed.
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	batchDir := filepath.Join(tmp, "batches")
	writeSyncBatch(t, batchDir, []SyncRecord{
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "user", Text: "importneedle question", Time: base},
	})
	if n, err := Import(dir, batchDir); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	// The plain index every reader runs next must ingest the local session.
	if err := EnsureForSearch(dir, search.Options{Query: "localneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "localneedle", All: true}); err != nil {
		t.Fatal(err)
	} else if len(ss) != 1 {
		t.Fatalf("local session lost after import-then-index: got %d", len(ss))
	}
	// And the imported records are still there.
	if ss, err := Search(dir, search.Options{Query: "importneedle", All: true}); err != nil {
		t.Fatal(err)
	} else if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") {
		t.Fatalf("imported record lost: %#v", ss)
	}
}
