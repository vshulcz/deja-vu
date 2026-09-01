package digest

import (
	"strings"
	"testing"
)

// A session whose only conclusion is one sentence longer than the budget
// answered a caller asking for three lines with silence, and a caller asking
// for one with the sentence, marked. The rule behind the marked cut is that
// nothing follows the marker — and an empty result is that whatever max was
// asked for, because the loop breaks straight after.
//
// 9 of 452 sessions on a real store settled something and got no block at all.
func TestOneOverlongConclusionIsMarkedRatherThanDropped(t *testing.T) {
	// Two sentences, the first of them alone longer than the budget. Both
	// parts matter: with one sentence firstSentences never counts two and
	// falls into its own 240-byte cap, and with a short first sentence the
	// one-sentence retry fits and no cut is needed. Either way the marked cut
	// is never reached and the test would be measuring something else.
	long := "The fix was raising pgbouncer default_pool_size to 40 " +
		strings.Repeat("after weighing the options against the replica load ", 8) +
		". Revisit when the replica catches up."

	for _, max := range []int{1, 3} {
		got := Conclusions(conclusionSession(long), 300, max)
		if len(got) == 0 {
			t.Fatalf("max=%d: a session that settled something got no block", max)
		}
		if !strings.HasSuffix(got[0], "…") {
			t.Errorf("max=%d: the line was not marked as cut:\n%s", max, got[0])
		}
		if len(got[0]) > 300 {
			t.Errorf("max=%d: the cut line is %d bytes, over the 300 budget", max, len(got[0]))
		}
	}
}

// The marked cut is a last resort, not the normal path: a conclusion that fits
// arrives whole and unmarked.
func TestAConclusionThatFitsIsNotMarked(t *testing.T) {
	got := Conclusions(conclusionSession("The fix was raising pgbouncer default_pool_size to 40."), 300, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if strings.HasSuffix(got[0], "…") {
		t.Errorf("a conclusion that fits was marked as cut:\n%s", got[0])
	}
}

// The marked cut is only for an empty result. Once a whole line is in, a cut
// must not follow it: the marker would read as "and there is more of this
// line" while the text after it is a different conclusion entirely (#1336).
func TestACutDoesNotFollowALineThatFitted(t *testing.T) {
	long := "The earlier attempt pinned pgx to 5.4.3 " +
		strings.Repeat("after weighing the options against the replica load ", 8) +
		". Revisit when the replica catches up."
	got := Conclusions(assistantSession(
		long,
		"The fix was raising pgbouncer default_pool_size to 40.",
	), 300, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "default_pool_size to 40") {
		t.Fatalf("the newest conclusion is not first, so this measures nothing:\n%s", got[0])
	}
	for i, l := range got[1:] {
		if strings.HasSuffix(l, "…") {
			t.Errorf("line %d is a marked cut following a line that fitted:\n%s", i+1, l)
		}
	}
}
