package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countNoteRecords is how many records a notes file holds.
func countNoteRecords(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(b))
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

// An identical re-promote writes nothing (#1319). Cleaning tags where they are
// written (#1811) put stored tags on one side of that comparison and freshly
// cleaned ones on the other, so a note written by an older binary — its tag
// carrying a control byte, or past the length cap — no longer compares equal.
// The result is one corrected record, once: the copy that lands holds the
// cleaned tags, and every promote after it is silent again (#1815).
func TestAnOldRecordIsRewrittenOnceAndThenSettles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now()

	// What a pre-#1811 binary wrote: the tags stored exactly as typed.
	old, err := json.Marshal(map[string]any{
		"ts": now.UTC().Format(time.RFC3339Nano), "project": "proj",
		"text": "we cap retries at three", "kind": "promoted", "session": "sess1",
		"state": "accepted", "title": "retries",
		"tags": []string{"red\x1b[31malert\x1b[0m", strings.Repeat("w", 400)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(old, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tags := []string{"red\x1b[31mALERT\x1b[0m", strings.Repeat("w", 400)}
	promote := func() {
		t.Helper()
		if err := AppendPromotedSourced("proj", "retries", "we cap retries at three", "sess1", "accepted", tags, time.Time{}, now); err != nil {
			t.Fatal(err)
		}
	}
	promote()
	if got := countNoteRecords(t, path); got != 2 {
		t.Fatalf("the first re-promote wrote %d records, want the one correction", got)
	}
	promote()
	promote()
	if got := countNoteRecords(t, path); got != 2 {
		t.Errorf("re-promoting kept appending: %d records after three runs", got)
	}
	// The record that landed carries cleaned tags — that is what makes the
	// next comparison equal.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Split(strings.TrimSpace(string(body)), "\n")[1], "\\u001b") {
		t.Errorf("the corrected record still carries an escape byte:\n%s", body)
	}

	// The control: a store this binary wrote stays at one record however often
	// the same decision is promoted, so the count above is the old data and
	// not the dedupe being broken.
	clean := filepath.Join(t.TempDir(), "clean.jsonl")
	t.Setenv("DEJA_NOTES_FILE", clean)
	promote()
	promote()
	promote()
	if got := countNoteRecords(t, clean); got != 1 {
		t.Errorf("an identical re-promote into a clean store wrote %d records", got)
	}
}
