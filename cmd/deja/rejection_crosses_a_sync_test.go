package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A decision marked rejected on one machine, exported and imported on another.
// The note arrives with its state inline; what the reader actually asks about
// is the transcript, and that used to arrive unmarked — so the peer was handed
// a decision that had been taken back, reading like current truth (#791, #895,
// the shape #1051 fixed for the ids the index carries).
func TestARejectionSurvivesAnExportAndImport(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	dir := filepath.Join(root, "-work-rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"t1","timestamp":"2026-07-01T10:00:00Z","cwd":"/work/rig",` +
		`"message":{"role":"user","content":"how do we trim the mizzen sail in a squall"}}`
	if err := os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "t1", "--state", "rejected", "--note", "squall trim advice was wrong"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if _, err := captureRun(t, "sync", "export", exp, "--full"); err != nil {
		t.Fatal(err)
	}

	// A second machine: a fresh home that has never seen the session, holding
	// only what the export carried.
	hermeticEnv(t)
	// The premise: a machine that has never seen this session and holds none of
	// the states, which live in the other machine's notes.jsonl and do not
	// travel.
	if _, err := os.Stat(os.Getenv("DEJA_NOTES_FILE")); err == nil {
		t.Fatalf("the peer already holds a notes file at %s", os.Getenv("DEJA_NOTES_FILE"))
	}
	if out, _ := captureRun(t, "--all", "mizzen sail"); strings.Contains(out, "mizzen sail") {
		t.Fatalf("the peer already holds the session:\n%s", out)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "--all", "mizzen sail")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mizzen sail") {
		t.Fatalf("the transcript did not come across at all:\n%s", out)
	}
	if !strings.Contains(out, "tried and rejected") {
		t.Errorf("the imported transcript carries no mark, so the peer is told a reverted decision still stands:\n%s", out)
	}
	if !strings.Contains(out, "squall trim advice was wrong") {
		t.Errorf("the correction did not travel with it:\n%s", out)
	}
}
