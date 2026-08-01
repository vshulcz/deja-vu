package search

import (
	"strings"
	"testing"
)

// A flat 12-character cut printed `deja-note-cl` for every promoted note and
// `00000000-000` for UUID sessions — twelve identical rows in one answer
// (#707).
func TestShortKeepsIDsDistinguishable(t *testing.T) {
	notes := []string{"deja-note-claude-s0089", "deja-note-claude-s0023", "deja-note-claude-s0139"}
	seen := map[string]bool{}
	for _, id := range notes {
		got := short(id)
		if seen[got] {
			t.Errorf("%q collides with an earlier id: %q", id, got)
		}
		seen[got] = true
		if !strings.HasSuffix(got, id[len(id)-4:]) {
			t.Errorf("short(%q) = %q — the distinguishing tail is gone", id, got)
		}
	}
	uuid := "00000000-0003-4a1b-9c2d-000000000003"
	if got := short(uuid); got == short("00000000-0002-4a1b-9c2d-000000000002") {
		t.Errorf("two UUIDs shortened to the same string: %q", got)
	}
	// Short ids are printed whole.
	for _, id := range []string{"", "s0001", "abc-123-xyz"} {
		if got := short(id); got != id {
			t.Errorf("short(%q) = %q, want it unchanged", id, got)
		}
	}
	// The line stays narrow: that is what the function is for.
	if got := short(strings.Repeat("x", 80)); len([]rune(got)) > 20 {
		t.Errorf("short() returned %d runes", len([]rune(got)))
	}
	// Runes, not bytes: cutting mid-character would print a replacement glyph.
	cyr := strings.Repeat("сессия", 6)
	if got := short(cyr); !strings.ContainsRune(got, '…') || strings.ContainsRune(got, '�') {
		t.Errorf("short(%q) = %q", cyr, got)
	}
}
