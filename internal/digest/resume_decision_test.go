package digest

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// What the marker rules let through, read across 32 projects on a real store:
// deja's own marker on a synced session, a one-word acknowledgement, and a
// pasted design document whose first line is a heading. Each carries a decision
// marker and none of them is a decision.
func TestWhatIsNotADecisionEvenWithTheMarkers(t *testing.T) {
	junk := []string{
		"Imported from Claude session `dccbfbb0-6b5a-466b-a180-ca1d9f658794`.",
		"Затащил.",
		"Done.",
		"## Goal - довести автоскейлер до 100/100: строгий аудит, чистая архитектура",
		"- fixed the retry budget and merged it",
	}
	for _, j := range junk {
		s := model.Session{Messages: []model.Message{{Role: "assistant", Text: j}}}
		if r := ResumeFrom(s, nil); r.Decision != "" {
			t.Errorf("kept as a decision: %q", r.Decision)
		}
	}
}

// And a sentence that settles something is still kept, heading-free and long
// enough to read cold.
func TestARealDecisionSurvivesTheseGuards(t *testing.T) {
	keep := []string{
		"решили: бюджет повторов для quokkabloom остаётся четыре",
		"we settled on warming the cold path before the first read",
	}
	for _, k := range keep {
		s := model.Session{Messages: []model.Message{{Role: "assistant", Text: k}}}
		if r := ResumeFrom(s, nil); r.Decision == "" {
			t.Errorf("a real decision was dropped: %q", k)
		}
	}
}
