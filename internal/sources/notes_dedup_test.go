package sources

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// The same conclusion saved twice — a long session, or two agents on one
// machine — used to land twice and cost the agent a line of every later recall
// for one fact (#1736).
func TestRememberingTheSameNoteTwiceIsANoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	when := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	if err := AppendNote("api", "pgbouncer needs prepared statements off", when); err != nil {
		t.Fatal(err)
	}
	err := AppendNote("api", "pgbouncer needs prepared statements off", when.Add(time.Minute))
	if !errors.Is(err, ErrNoteExists) {
		t.Errorf("a duplicate was accepted: %v", err)
	}
	// Same text, different project, is a different note.
	if err := AppendNote("web", "pgbouncer needs prepared statements off", when); err != nil {
		t.Errorf("another project was refused: %v", err)
	}
	// A near-duplicate is not a duplicate.
	if err := AppendNote("api", "pgbouncer needs prepared statements off for now", when); err != nil {
		t.Errorf("a longer note was refused: %v", err)
	}
	b, err := os.ReadFile(NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; n != 3 {
		t.Errorf("notes file holds %d rows, want 3:\n%s", n, b)
	}
}
