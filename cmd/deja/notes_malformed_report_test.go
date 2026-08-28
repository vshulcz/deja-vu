package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The notes file is the one store a person writes by hand, holding the one
// thing deja cannot re-derive, and a line it cannot read is dropped. #2005 made
// those refusals counted rather than silent; the counting is pinned for claude
// (internal/index/health_carry_test.go) and was never pinned for the store the
// hand-writing actually happens in — nor was the sentence a person sees.
func TestAHandWrittenNoteLineDejaCannotReadIsReported(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// One line in the shape the reader wants, and the shapes a person
	// plausibly writes instead: another key for the stamp and a date with no
	// time — both refused on the stamp — another key for the body, refused on
	// the empty text, and something that is not JSON, which the scanner under
	// the reader refuses before it ever gets there.
	lines := []string{
		`{"ts":"` + now + `","text":"the migration held the lock"}`,
		`{"id":"deja-note-1","time":"` + now + `","text":"the pool size is the fix"}`,
		`{"ts":"2026-01-03","text":"a date without a time"}`,
		`{"ts":"` + now + `","note":"wrong key for the body"}`,
		`not json at all`,
	}
	if err := os.WriteFile(notes, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	// The run says it, with the count, rather than indexing one note and
	// moving on.
	if !strings.Contains(said, "4 lines skipped") {
		t.Errorf("the index run does not name the lines it could not read:\n%s", said)
	}
	// On the store's own line, so a reader can carry the number from here to
	// doctor's JSON, where the same count is filed under the harness name.
	for _, line := range strings.Split(said, "\n") {
		if strings.Contains(line, "4 lines skipped") && !strings.Contains(line, "notes:") {
			t.Errorf("the count is not on the notes store's line, so nothing says whose lines they are:\n%s", line)
		}
	}
	// The note deja could read is there, so the refusals are the four and not
	// the whole file.
	if out, _ := captureRun(t, "search", "migration"); !strings.Contains(out, "migration held the lock") {
		t.Fatalf("the readable note is not in the index, so this measures nothing:\n%s", out)
	}
	if out, _ := captureRun(t, "search", "pool size"); strings.Contains(out, "pool size is the fix") {
		t.Errorf("a line deja said it could not read is in the index anyway:\n%s", out)
	}

	// doctor carries it too: the sentence a person lands on, and the count
	// under the source it belongs to.
	doc, _ := captureRun(t, "doctor")
	if !strings.Contains(doc, "unusable lines skipped") {
		t.Errorf("doctor does not mention the lines:\n%s", doc)
	}
	raw, _ := captureRun(t, "doctor", "--json")
	var report struct {
		Ingest map[string]struct {
			MalformedLines int `json:"malformed_lines"`
		} `json:"ingest_health"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json does not parse: %v", err)
	}
	total := 0
	for _, h := range report.Ingest {
		total += h.MalformedLines
	}
	if total != 4 {
		t.Errorf("doctor --json reports %d malformed lines across %d sources, want 4", total, len(report.Ingest))
	}
	// Under the harness the notes file belongs to, which is "deja" — the index
	// run calls the same store "notes", and a reader crossing from one to the
	// other has to know they are one thing.
	if n := report.Ingest["deja"].MalformedLines; n != 4 {
		t.Errorf("the notes store reports %d of them; a person fixing the file needs to know they are theirs", n)
	}
}
