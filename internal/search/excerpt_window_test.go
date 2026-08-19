package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The excerpt is 300 runes around the match. When the match sat near the end
// of a message the window ran off it and the rest of the budget went unspent:
// 120 runes where the same match in the middle showed 302 — and the end of a
// long output is where the answer sits as often as anywhere (#1319).
func TestTheExcerptSpendsItsWindowWhereverTheMatchIs(t *testing.T) {
	filler := strings.Repeat("filler about unrelated work, ", 30)
	for _, tc := range []struct{ name, msg string }{
		{"at the start", "the retry queue stalls " + filler},
		{"in the middle", filler + " the retry queue stalls " + filler},
		{"at the end", filler + " the retry queue stalls"},
	} {
		got := utf8.RuneCountInString(snippet(tc.msg, "retry queue", nil))
		if got < 290 {
			t.Errorf("%s: excerpt is %d runes of the 300 the message can fill", tc.name, got)
		}
	}
}

// The clamp at the other end is untouched: a match inside the first hundred
// runes of a message shorter than the window still starts at the beginning,
// with no mark claiming something was dropped in front of it.
func TestAMatchNearTheStartIsUnchanged(t *testing.T) {
	msg := "the retry queue stalls on staging " + strings.Repeat("and then it recovered, ", 6)
	if n := utf8.RuneCountInString(msg); n >= 300 {
		t.Fatalf("the fixture is %d runes, so it does not exercise the clamp", n)
	}
	got := snippet(msg, "retry queue", nil)
	if strings.HasPrefix(got, "…") {
		t.Errorf("a match at the start was marked as cut in front: %q", got)
	}
	if !strings.HasPrefix(got, "the retry queue") {
		t.Errorf("the excerpt does not begin at the beginning: %.40s", got)
	}
}

// A message shorter than the window is shown whole, with no marks claiming
// something was cut.
func TestAShortMessageIsShownWhole(t *testing.T) {
	msg := "the retry queue stalls on staging when the workers wake together"
	got := snippet(msg, "retry queue", nil)
	if got != msg {
		t.Errorf("a short message was changed:\n%q\n%q", got, msg)
	}
	if strings.Contains(got, "…") {
		t.Errorf("a short message was marked as cut: %q", got)
	}
}

// The match itself stays inside the window wherever it sits.
func TestTheMatchStaysInTheExcerpt(t *testing.T) {
	filler := strings.Repeat("filler about unrelated work, ", 30)
	for _, msg := range []string{
		"the retry queue stalls " + filler,
		filler + " the retry queue stalls " + filler,
		filler + " the retry queue stalls",
		filler + " the retry queue stalls " + strings.Repeat("x", 40),
	} {
		if got := snippet(msg, "retry queue", nil); !strings.Contains(got, "retry queue") {
			t.Errorf("the match fell outside the excerpt: %.80s", got)
		}
	}
}
