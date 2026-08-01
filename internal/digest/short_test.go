package digest

import (
	"strings"
	"testing"
)

// A flat cut printed "deja-2026-08" for a note whose id is
// "deja-2026-08-01-ops/db" — an id no session has, in the error telling the
// reader which session was refused (#741).
func TestShortKeepsBothEnds(t *testing.T) {
	long := "deja-2026-08-01-ops/db"
	got := Short(long)
	if !strings.HasPrefix(got, "deja-2026") || !strings.HasSuffix(got, "ops/db") {
		t.Errorf("Short(%q) = %q — an end is missing", long, got)
	}
	if len([]rune(got)) > 20 {
		t.Errorf("Short returned %d runes: %q", len([]rune(got)), got)
	}
	// Two ids that differ only in the middle must not collapse together.
	if Short("deja-2026-08-01-ops/db") == Short("deja-2026-09-14-ops/db") {
		t.Error("two ids shortened to the same string")
	}
	// Short ids are printed whole.
	for _, id := range []string{"", "a1", "deja-note-claude-a1"} {
		if got := Short(id); got != id {
			t.Errorf("Short(%q) = %q, want it unchanged", id, got)
		}
	}
	// Runes, not bytes: cutting mid-character would print a replacement glyph.
	cyr := strings.Repeat("сессия", 6)
	if got := Short(cyr); strings.ContainsRune(got, '�') {
		t.Errorf("Short(%q) = %q", cyr, got)
	}
}
