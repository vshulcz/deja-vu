package sources

import (
	"path/filepath"
	"testing"
	"time"
)

// promote writes a record every run, so re-promoting an unchanged decision
// used to append a duplicate — the note then carried the same line N times and
// each copy lifted its own weight in recall. An identical re-promote must be a
// no-op on disk; a genuine change (state, text, tags) still appends.
func TestIdenticalRepromoteDoesNotGrowTheNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	src := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	appendOnce := func(state, text, title string, tags []string) {
		if err := AppendPromotedSourced("proj", title, text, "claude:c1", state, tags, src, time.Now()); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	noteMessages := func() int {
		ss, err := ParseNotesFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			if s.ID == PromotedNoteID("claude:c1") {
				return len(s.Messages)
			}
		}
		return 0
	}

	// Five identical promotes collapse to one message.
	for i := 0; i < 5; i++ {
		appendOnce("accepted", "pool exhausted", "davit", []string{"perf"})
	}
	if got := noteMessages(); got != 1 {
		t.Fatalf("identical re-promotes grew the note: got %d messages, want 1", got)
	}

	// A state change is a correction — it appends.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf"})
	if got := noteMessages(); got != 2 {
		t.Fatalf("state change did not append: got %d messages, want 2", got)
	}
	// Repeating the rejected state does not grow it again.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf"})
	if got := noteMessages(); got != 2 {
		t.Fatalf("identical rejected re-promote grew the note: got %d, want 2", got)
	}
	// A new tag is a change — it appends.
	appendOnce("rejected", "pool exhausted", "davit", []string{"perf", "urgent"})
	if got := noteMessages(); got != 3 {
		t.Fatalf("tag change did not append: got %d messages, want 3", got)
	}
}
