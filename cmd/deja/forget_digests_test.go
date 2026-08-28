package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// forget drops the messages and adds tombstones; the injection log kept the
// digests served from them, so `deja view` republished forgotten text with no
// trust rule in play (#2325).
func TestForgetTakesTheDigestsOfWhatItForgot(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peersess", Project: "secretclient/api",
		Role: "assistant", Text: "the quaxbolt overflow was an int32 cast",
		Time: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. the quaxbolt overflow was an int32 cast\n", 1, 400, nil,
		[]string{"imported:secretclient/api"}, "local+imported")
	usage.RecordServedFrom(dir, usage.KindRecall,
		"<deja-recall>\n1. my own connection pool question\n", 1, 400, nil,
		[]string{"tmp/mine"}, "local+imported")

	if _, err := captureRun(t, "forget", "--project", "imported:secretclient/api"); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "int32 cast") {
		t.Errorf("the forgotten project's digest is still in the injection log")
	}
	if !strings.Contains(string(log), "my own connection pool question") {
		t.Errorf("forget took a digest that belongs to another project")
	}
	// The events stay: they record that something was served, which is what
	// the counters read.
	if events := usage.Events(dir, 0); len(events) != 2 {
		t.Errorf("events = %d, want both — an event is what happened, not the content", len(events))
	}
}

// --dry-run says what it would take and takes nothing.
func TestForgetDryRunLeavesTheDigestsAlone(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"a question about pools"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordServedFrom(dir, usage.KindRecall, "<deja-recall>\n1. a question about pools\n", 1, 400, nil,
		[]string{"tmp/mine"}, "")

	out, err := captureRun(t, "forget", "--project", "tmp/mine", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "digest") {
		t.Errorf("the dry run does not say the digests would go:\n%s", out)
	}
	log, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "a question about pools") {
		t.Errorf("a dry run removed a digest")
	}
}
