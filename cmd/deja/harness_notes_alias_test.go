package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The index run narrates the notes store as "notes", and filtering by the name
// it just printed was refused as a typo: `checkHarness` (#1113) rejects the
// name before retrieval's own alias (#1888) can accept it. Both gates now agree
// (#2191).
func TestFilteringByTheNameTheIndexRunPrintsForNotes(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	line := `{"ts":"` + now + `","text":"the kafka consumer rebalances when the heartbeat is late"}`
	if err := os.WriteFile(notes, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this is the name the run prints, or the filter is being
	// asked to accept something nobody was shown.
	if !strings.Contains(said, "notes:") {
		t.Fatalf("the index run does not call the store notes, so this measures nothing:\n%s", said)
	}
	// And the note is searchable under the stored name, so a miss below is the
	// filter and not an empty index.
	if out, _ := captureRun(t, "search", "rebalances", "--harness", "deja"); !strings.Contains(out, "heartbeat is late") {
		t.Fatalf("the note is not searchable under its stored name:\n%s", out)
	}

	out, err := captureRun(t, "search", "rebalances", "--harness", "notes")
	if err != nil {
		t.Fatalf("search --harness notes failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "heartbeat is late") {
		t.Errorf("search --harness notes does not reach the notes the run just counted:\n%s", out)
	}
	stats, err := captureRun(t, "stats", "--harness", "notes")
	if err != nil {
		t.Fatalf("stats --harness notes failed: %v\n%s", err, stats)
	}
	// The one session in the index is the note, and stats compares the stored
	// name exactly, so counting it is the whole question.
	if !strings.Contains(stats, "1 session indexed") {
		t.Errorf("stats --harness notes does not count the note the run just indexed:\n%s", stats)
	}
	// An empty result names the flag the reader passed, not the name deja
	// stores it under — the same rule the --since echo follows.
	empty, _ := captureRunStderr(t, "search", "zzzznothing", "--harness", "notes")
	if strings.Contains(empty, `harness "deja"`) || !strings.Contains(empty, `harness "notes"`) {
		t.Errorf("the empty result names a filter the reader did not pass:\n%s", empty)
	}
	// A real typo is still a typo.
	if _, err := captureRun(t, "search", "rebalances", "--harness", "notez"); err == nil {
		t.Errorf("an unknown harness is accepted, so the check no longer tells a typo from a name")
	}
}
