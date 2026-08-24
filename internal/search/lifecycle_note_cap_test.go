package search

import (
	"strings"
	"testing"
)

// A promote note is free text a person wrote, and every surface printed it in
// full on one line: a 4000-character note came out as a 4051-column line on a
// screen budgeted to 80, and as 4074 of 4347 bytes in an agent's context
// answer (#1645).
func TestLifecycleNoteIsClippedLikeEveryOtherLine(t *testing.T) {
	long := strings.Repeat("x", 4000)
	h := Hit{Lifecycle: "rejected", LifecycleAt: "2026-08-24", LifecycleNote: long}
	got := lifecycleSummary(h)
	if len(got) > answerCap+80 {
		t.Errorf("the search screen prints %d characters for one note", len(got))
	}
	b := BlameLifecycleLine(BlameHit{Lifecycle: "rejected", LifecycleAt: "2026-08-24", LifecycleNote: long})
	if len(b) > answerCap+80 {
		t.Errorf("blame prints %d characters for one note", len(b))
	}
	// The controls: a short note survives whole, and the state still reads.
	short := "backed out, the pool was not the problem"
	got = lifecycleSummary(Hit{Lifecycle: "rejected", LifecycleAt: "2026-08-24", LifecycleNote: short})
	if !strings.Contains(got, short) || !strings.Contains(got, "tried and rejected") {
		t.Errorf("a short note or its state was lost: %q", got)
	}
	if b = BlameLifecycleLine(BlameHit{Lifecycle: "stale", LifecycleNote: short}); !strings.Contains(b, short) {
		t.Errorf("blame lost a short note: %q", b)
	}
}
