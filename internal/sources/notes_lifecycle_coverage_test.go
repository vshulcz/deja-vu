package sources

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A promoted note borrows its source session's opening line as a title. forget
// strips that title (#666); unforget restores it (#969); forgetting the note
// itself drops the line. Exercise the whole round trip.
func TestPromotedNoteTitleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)

	src := "claude:sess1"
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := AppendPromotedSourced("proj", "Borrowed Title", "the decision text", src, "accepted", nil, time.Time{}, now); err != nil {
		t.Fatal(err)
	}

	titleOf := func() (string, bool) {
		ss, err := ParseNotesFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			if s.ID == PromotedNoteID(src) {
				return s.Title, true
			}
		}
		return "", false
	}

	if title, ok := titleOf(); !ok || !strings.Contains(title, "Borrowed Title") {
		t.Fatalf("after promote, title = %q (present=%v)", title, ok)
	}

	// forget strips the borrowed title.
	n, err := ForgetPromotedTitles(func(s string) bool { return s == src })
	if err != nil || n != 1 {
		t.Fatalf("ForgetPromotedTitles = %d, %v", n, err)
	}
	if title, ok := titleOf(); !ok || strings.Contains(title, "Borrowed Title") {
		t.Fatalf("title survived forget: %q", title)
	}

	// unforget restores it from the source session.
	n, err = RestorePromotedTitles(func(s string) string {
		if s == src {
			return "Restored Title"
		}
		return ""
	})
	if err != nil || n != 1 {
		t.Fatalf("RestorePromotedTitles = %d, %v", n, err)
	}
	if title, ok := titleOf(); !ok || !strings.Contains(title, "Restored Title") {
		t.Fatalf("title not restored: %q", title)
	}

	// forgetting the note itself removes the line entirely.
	n, err = ForgetPromotedNotes(func(id string) bool { return id == PromotedNoteID(src) })
	if err != nil || n != 1 {
		t.Fatalf("ForgetPromotedNotes = %d, %v", n, err)
	}
	if title, ok := titleOf(); ok {
		t.Fatalf("note survived ForgetPromotedNotes: %q", title)
	}
}

func TestPromotedNoteHelpersNoMatchChangeNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	if err := AppendPromotedSourced("proj", "Title", "text", "claude:keep", "accepted", nil, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, got := range []int{
		mustCount(t, func() (int, error) { return ForgetPromotedTitles(func(string) bool { return false }) }),
		mustCount(t, func() (int, error) { return ForgetPromotedNotes(func(string) bool { return false }) }),
		mustCount(t, func() (int, error) { return RestorePromotedTitles(func(string) string { return "" }) }),
	} {
		if got != 0 {
			t.Errorf("a no-match rewrite changed %d lines, want 0", got)
		}
	}
}

func mustCount(t *testing.T, f func() (int, error)) int {
	t.Helper()
	n, err := f()
	if err != nil {
		t.Fatal(err)
	}
	return n
}
