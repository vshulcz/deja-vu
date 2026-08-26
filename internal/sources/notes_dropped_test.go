package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A note is the one class of content deja cannot re-derive from anything else,
// and the notes file is the one store a person can write by hand. A line the
// parser refuses has to be counted, or it is gone with nothing to find it by:
// not in the index, not in the run's narration, not in doctor (#2005). The
// promoted branch has counted its own refusals since #814.
func TestANoteThatCannotBeReadIsCounted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	body := strings.Join([]string{
		`{"ts":"2026-01-02T03:04:05Z","project":"app","text":"pgbouncer pool size is 40"}`,
		// A date without a time, which is what a person writes by hand.
		`{"ts":"2026-01-03","project":"app","text":"the retry budget is 3"}`,
		// No timestamp at all.
		`{"project":"app","text":"the pool lives in pool.go"}`,
		// Nothing to keep, but still a line someone wrote.
		`{"ts":"2026-01-04T03:04:05Z","project":"app","text":"   "}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	DiagSnapshot() // drop whatever another case left behind
	ss, err := ParseNotesFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the good note is kept and the other three are not, or the
	// count below is about something else.
	msgs := 0
	for _, s := range ss {
		msgs += len(s.Messages)
	}
	if msgs != 1 {
		t.Fatalf("four notes in, %d messages out: the fixture is not the one this is about", msgs)
	}

	malformed, _ := DiagSnapshot()
	if got := malformed[path]; got != 3 {
		t.Errorf("three notes were dropped and %d were counted, so a hand-written note can vanish with no trace", got)
	}
}
