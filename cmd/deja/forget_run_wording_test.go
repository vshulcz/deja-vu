package main

import (
	"strings"
	"testing"
)

// The dry run and the run that acts must answer the same question the same
// way. #957 taught the sentence to tell a `remember` note from a promoted one
// and wired it into the `--dry-run` branch alone, so the run that actually
// dropped a day of notes called them promoted — the wrong answer, on the path
// that changes something, checked against the right answer one command earlier
// (#3599).
func TestForgetSaysTheSameThingWhetherOrNotItActs(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "remember", "the queue drains only after the worker restarts"); err != nil {
		t.Fatal(err)
	}
	id := onlyNoteID(t)

	dry, err := captureRun(t, "forget", "--session", id, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	said, err := captureRun(t, "forget", "--session", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ label, out string }{{"dry run", dry}, {"the run that acts", said}} {
		if !strings.Contains(c.out, "your own notes") {
			t.Errorf("%s does not call a remember note what it is:\n%s", c.label, c.out)
		}
		if strings.Contains(c.out, "promoted note") {
			t.Errorf("%s calls a remember note promoted, which the reader never did:\n%s", c.label, c.out)
		}
	}
}

// onlyNoteID is the id of the single note the store holds, read the way a
// reader would: off the search result rather than out of the manifest.
func onlyNoteID(t *testing.T) string {
	t.Helper()
	out, err := captureRun(t, "search", "queue drains", "--json")
	if err != nil {
		t.Fatal(err)
	}
	const key = `"id":"`
	_, after, ok := strings.Cut(out, key)
	if !ok {
		t.Fatalf("the note is not in the index:\n%s", out)
	}
	rest := after
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unreadable hit:\n%s", out)
	}
	return rest[:j]
}
