package main

import (
	"strings"
	"testing"
)

// Removing a note promoted by mistake is `forget` on the note's own id, and
// nothing said so: the reader who types `unpromote` was sent back to the
// command that made the note, and the --state refusal explained marks (#1085).
func TestTheUndoOfAPromoteIsNamed(t *testing.T) {
	for _, word := range []string{"unpromote", "demote"} {
		got := commandHint(word)
		if !strings.Contains(got, "deja forget --session deja-note-") {
			t.Errorf("%q: the removal path is not named: %q", word, got)
		}
		if !strings.Contains(got, "--state rejected") {
			t.Errorf("%q: taking the decision back is not named: %q", word, got)
		}
	}
	// A word that means something else keeps its own redirect.
	if got := commandHint("unforget"); !strings.Contains(got, "--unforget <id>") {
		t.Errorf("unforget lost its own hint: %q", got)
	}
}
