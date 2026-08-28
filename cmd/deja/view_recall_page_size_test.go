package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The page bounds its transcripts (viewPreviewBytes) and its notes (#2111).
// Digest text has no bound of its own: what keeps it small is the injection
// log's own rotation, which rewrites past 512 KB keeping the last records — so
// however much is written, the tab can only carry what the log still holds.
//
// This holds the page to that. Four megabytes of digests go in; the page stays
// one fast file, and the log is left with a handful of records rather than the
// five hundred that were written.
func TestThePageIsBoundedByTheInjectionLogsRotation(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Each digest the size of the largest a writer records — a whole-session
	// read over deja://session/… measured 8,216 bytes.
	body := strings.Repeat("the migration kept failing on the ledger table. ", 170)
	const wrote = 500
	for i := 0; i < wrote; i++ {
		usage.RecordDigestPolicy(dir, usage.KindResource, "<deja-recall>\n"+body+"\n", 1, 400, "local+imported")
	}

	kept := usage.Snapshots(dir, 0)
	if len(kept) >= wrote {
		t.Errorf("the log kept %d of %d digests — rotation is what bounds the page", len(kept), wrote)
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The bound the notes cap is held to.
	if n := len(page); n > 3<<20 {
		t.Errorf("the page is %d bytes after %d digests were written", n, wrote)
	}
	if !strings.Contains(string(page), "the migration kept failing") {
		t.Fatal("no digest text reached the page, so this measures nothing")
	}
}
