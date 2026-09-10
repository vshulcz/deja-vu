package digest

import "testing"

// The heading arrives emphasised as often as plain, and the shape was matched
// without allowing for it — so the same block counted as a person's words half
// the time. Found where it costs most: `promote` kept "asked: Summary: 1.
// **Primary Request and Intent:** …" as what a session was asked to do.
func TestAnEmphasisedCompactionHeadingIsStillOne(t *testing.T) {
	summaries := []string{
		"Summary:\n1. Primary Request and Intent:\n   move the quokkabloom fetcher off the old queue",
		"Summary: 1. **Primary Request and Intent:** The goal was moving the fetcher off the old queue",
		"Summary:\n1. __Primary Request and Intent:__ the fetcher moves off the queue",
	}
	for _, s := range summaries {
		if !IsCompactionSummary(s) {
			t.Errorf("a compaction summary read as something a person wrote:\n  %s", s)
		}
	}
}

// And a person asking about one is still a person.
func TestAQuestionAboutASummaryIsNotOne(t *testing.T) {
	asked := []string{
		"Summary: what is the Primary Request and Intent here?",
		"summary of the primary request: we move off the queue",
	}
	for _, a := range asked {
		if IsCompactionSummary(a) {
			t.Errorf("a question was read as a compaction summary:\n  %s", a)
		}
	}
}
