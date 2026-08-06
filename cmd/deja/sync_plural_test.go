package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The first line after a sync, and the line on the card people post. #737 put
// pluralS in for exactly this and neither surface used it: one imported
// session printed "imported 1 records — 1 sessions from another machine" and
// "1 sessions indexed" (#1052).
func TestSyncAndStatsCountOneSessionInTheSingular(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	batch := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"harness":"claude","session_id":"one-1","project":"p","role":"user","text":"only one record here","time":"2026-01-02T06:04:05+03:00"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-one.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "sync", "import", batch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported 1 record —") {
		t.Errorf("the import line counts one record as many:\n%s", out)
	}
	if !strings.Contains(out, "1 session from another machine") {
		t.Errorf("the import line counts one session as many:\n%s", out)
	}

	if got := statsHeadline(stats.Report{TotalSessions: 1}); got != "1 session indexed" {
		t.Errorf("the card headline reads %q", got)
	}
	if got := statsHeadline(stats.Report{TotalSessions: 2}); got != "2 sessions indexed" {
		t.Errorf("the plural case broke: %q", got)
	}
}

// The own-copy line counted one record with a plural verb: "1 record were
// already here", the same slip #1052 fixed on the import and stats lines.
func TestTheOwnCopyLineAgreesWithItsCount(t *testing.T) {
	if got := ownCopyLine(1); !strings.Contains(got, "1 record was already here") {
		t.Errorf("one record: %q", got)
	}
	if got := ownCopyLine(2); !strings.Contains(got, "2 records were already here") {
		t.Errorf("two records: %q", got)
	}
}
