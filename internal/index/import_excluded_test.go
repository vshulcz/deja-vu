package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A batch whose every record belongs to a project the receiver excludes is
// dropped — correctly, since the exclude list keeps a project out of this
// machine's memory and a sync must not put it back. Nothing counted it, so
// "imported 0 records" read like an empty or already-synced batch, the misread
// #1118 recorded for the drop one line above (#2666).
func TestImportCountsWhatTheExcludeListKeptOut(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// This package shares one XDG_CONFIG_HOME across its tests and
	// sources.ExcludePath() reads it, so a list written through that path
	// outlives the test — the trap #2654 hit with the policy file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, id := range []string{"a1", "a2", "a3"} {
		body += `{"harness":"claude","session_id":"` + id + `","project":"alpha","role":"user","text":"the zonkobuffer keeps overflowing","time":"2026-07-01T10:00:00Z","origin":"mini"}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	excl := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excl, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "index.db")
	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the excluded project must not be imported, got %d records", n)
	}
	if got := ImportSkippedExcluded(); got != 3 {
		t.Fatalf("three records were dropped by the reader's own pattern, the count says %d", got)
	}
}

// And a batch nothing excludes leaves the counter at zero, so the line only
// appears when it has something to report.
func TestImportCountsNothingExcludedWhenNoPatternMatches(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// This package shares one XDG_CONFIG_HOME across its tests and
	// sources.ExcludePath() reads it, so a list written through that path
	// outlives the test — the trap #2654 hit with the policy file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"harness":"claude","session_id":"a1","project":"alpha","role":"user","text":"the zonkobuffer keeps overflowing","time":"2026-07-01T10:00:00Z","origin":"mini"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "index.db")
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	if got := ImportSkippedExcluded(); got != 0 {
		t.Fatalf("nothing was excluded, the count says %d", got)
	}
}
