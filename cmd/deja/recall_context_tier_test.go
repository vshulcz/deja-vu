package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// recall_context returns the most text of any tool — a whole session — and
// said what it was in one bracketed word. `[relevance]` above a session that
// matched nothing reads as a label on an answer, where recall says it in a
// sentence (#2787, the shape #2074 fixed for the counted page).
func TestTheContextLeadSaysWhatTheSessionIs(t *testing.T) {
	lead := contextTierLead(search.TierRelevance)
	if strings.HasPrefix(lead, "[") {
		t.Errorf("a whole session is still introduced by a marker: %q", lead)
	}
	for _, want := range []string{"No session is about this", "nearest by wording", "not as a record"} {
		if !strings.Contains(lead, want) {
			t.Errorf("the lead does not say %q: %q", want, lead)
		}
	}
}

// The other tiers keep their marker: they did match, and the marker says how.
func TestTheOtherTiersKeepTheirMarker(t *testing.T) {
	for _, tier := range []string{search.TierError, search.TierSemantic, search.TierStemmed, search.TierClose} {
		lead := contextTierLead(tier)
		if !strings.HasPrefix(lead, "["+tier+"]") {
			t.Errorf("%s lost its marker: %q", tier, lead)
		}
	}
}

// End to end, because the wiring is where the risk was: the lead has to reach
// the agent, inside the frame, with the tier marker gone.
func TestAnAgentAskingAboutSomethingAbsentIsToldSo(t *testing.T) {
	dir := manySessionStore(t, 40)
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"stalled retry frames parser pipeline empty"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "["+search.TierRelevance+"]") {
		t.Errorf("a whole session still arrives under a bare marker:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, nothingIsAboutThis) {
		t.Errorf("the agent is not told the session is not about its question:\n%s", firstLines(text, 4))
	}
}

// One sentence, two surfaces: the page of sessions and the single session say
// the same thing about the same situation.
func TestBothRelevanceLeadsShareTheirSentence(t *testing.T) {
	if !strings.Contains(contextTierLead(search.TierRelevance), nothingIsAboutThis) {
		t.Error("the single-session lead drifted from the shared sentence")
	}
	dir := manySessionStore(t, 40)
	text, err := callMCPTool(dir, "recall", []byte(`{"query":"stalled retry frames parser pipeline empty"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, nothingIsAboutThis) {
		t.Errorf("the page of sessions drifted from the shared sentence:\n%s", firstLines(text, 4))
	}
}
