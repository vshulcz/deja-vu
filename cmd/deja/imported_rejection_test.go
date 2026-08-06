package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A decision rejected on one machine and synced to another: the note arrives
// carrying its state, the transcript it rejects arrives beside it, and only
// the note was marked. The query a person types finds the transcript, so a
// reverted decision came back reading like current truth — #974 again, this
// time across a sync (#1051).
func TestASyncedRejectionMarksTheTranscriptItRejects(t *testing.T) {
	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	// What `deja sync export` writes on the machine that made the decision:
	// the transcript, the note, and the note's correction. Ids are the sender's.
	recs := strings.Join([]string{
		`{"harness":"claude","session_id":"src1","project":"proj/p","role":"user","text":"we will cap the connection pool at 20","time":"2026-08-04T17:44:54+03:00"}`,
		`{"harness":"deja","session_id":"deja-note-claude-src1","project":"proj/p","role":"user","text":"[accepted] capped the pool at 20 (from claude:src1, 2026-08-06)","time":"2026-08-06T17:44:54+03:00"}`,
		`{"harness":"deja","session_id":"deja-note-claude-src1","project":"proj/p","role":"user","text":"[rejected] reverted: pgbouncer instead (from claude:src1, 2026-08-06)","time":"2026-08-06T17:45:04+03:00"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "sync", "import", batch); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	hits := searchHits(t, dir, "connection pool")
	if len(hits) == 0 {
		t.Fatal("the imported transcript did not come back at all")
	}
	// The transcript is the hit this query returns; the note's own words
	// ("pgbouncer") are not in it, which is the whole point of the bug.
	transcript := -1
	for i, h := range hits {
		if h.Session.Harness == "claude" {
			transcript = i
		}
	}
	if transcript < 0 {
		t.Fatalf("no transcript hit: %#v", hits)
	}
	attachLifecycles(dir, hits)
	if got := hits[transcript].Lifecycle; got != "rejected" {
		t.Errorf("imported transcript carries lifecycle %q, want rejected", got)
	}
	if line := lifecycleLine(hits[transcript]); !strings.Contains(line, "tried and rejected") {
		t.Errorf("the transcript's line does not say what happened: %q", line)
	}
	if got := hits[transcript].LifecycleNote; got != "reverted: pgbouncer instead (from claude:src1, 2026-08-06)" {
		t.Errorf("correction lost on the way: %q", got)
	}
}
