package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Conclusions are what an agent reads instead of the session. Fitting the last
// one by cutting it mid-sentence left a line that reads as a finished thought
// while the part that fell off the end was the end of it — and the end is where
// a session says it changed its mind.
func TestConclusionsDoNotEndMidSentence(t *testing.T) {
	long := "we moved the scheduler into its own process " + strings.Repeat("after weighing the options ", 12) + "and then reverted that."
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "we capped retries at three " + strings.Repeat("having weighed the options ", 12) + "and left it there."},
		{Role: "assistant", Text: long},
	}}
	for _, c := range Conclusions(s, 400, 2) {
		// A sentence, or a cut this project marks as one. What must not happen
		// is a line that simply stops.
		c = strings.TrimSpace(c)
		if !strings.HasSuffix(c, ".") && !strings.HasSuffix(c, "…") {
			t.Errorf("conclusion ends mid-sentence with nothing to say so: %q", c)
		}
	}
}

// A conclusion that fits whole is still returned whole, and the budget still
// bounds the total — otherwise a fix could pass the test above by dropping
// every conclusion that needs the budget checked at all.
func TestConclusionsStillFillTheBudget(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "we capped retries at three. the write-up is in the ticket."},
		{Role: "assistant", Text: "we moved the scheduler into its own process. it has run clean since."},
	}}
	got := Conclusions(s, 400, 2)
	if len(got) != 2 {
		t.Fatalf("got %d conclusions, want both: %q", len(got), got)
	}
	// A conclusion that fits keeps its second sentence: the budget shortens
	// what does not fit, not everything.
	for _, c := range got {
		if strings.Count(c, ".") < 2 {
			t.Errorf("conclusion lost its second sentence though it fit: %q", c)
		}
	}
	total := 0
	for _, c := range got {
		total += len(c)
	}
	if total > 400 {
		t.Errorf("conclusions total %d bytes, over the 400 budget", total)
	}
}

// When the line that does not fit has a first sentence that does, that sentence
// is what gets kept — the budget shortens the conclusion instead of deleting it.
func TestConclusionsFallBackToTheFirstSentence(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "we capped retries at three " + strings.Repeat("having weighed the options ", 12) + "and left it there."},
		{Role: "assistant", Text: "we moved the scheduler out. " + strings.Repeat("the write-up is above ", 20) + "and nothing else changed."},
	}}
	got := Conclusions(s, 400, 2)
	if len(got) != 2 {
		t.Fatalf("got %d conclusions, want the shortened one kept: %q", len(got), got)
	}
	if !strings.HasPrefix(got[1], "we capped retries at three") {
		t.Errorf("second conclusion is %q, not the first sentence of the one that did not fit", got[1])
	}
}
