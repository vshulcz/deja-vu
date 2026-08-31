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
	lead := contextTierLead(search.TierRelevance, false)
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
		lead := contextTierLead(tier, false)
		if !strings.HasPrefix(lead, "["+tier+"]") {
			t.Errorf("%s lost its marker: %q", tier, lead)
		}
	}
}

// The end-to-end case this file used to carry as
// TestAnAgentAskingAboutSomethingAbsentIsToldSo is gone, and its two halves are
// covered with premises that hold: the bare marker by
// TestTheContextLeadSaysWhatTheSessionIs above, and "the agent is told when the
// session is not about its question" by
// TestARelevanceAnswerAboutSomethingAbsentKeepsTheCaveat, which builds a store
// where that case actually happens.
//
// Its query, "stalled retry frames parser pipeline empty", is not an absent
// subject: every one of those words is in the fixture, spread across sessions
// that each hold some, which is why the relevance tier answers. On this
// fixture a genuinely absent subject is served nothing at all — the right
// answer, and not the one that test was written to check (#2827).

// And the query that test used to carry: words the store does hold, no session
// holding all of them. The tier is the same and the answer is not — the served
// session says the rarest thing asked about, so it is not disowned (#2827).
func TestAMashOfWordsTheStoreHoldsIsNotCalledAbsent(t *testing.T) {
	dir := manySessionStore(t, 40)
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"stalled retry frames parser pipeline empty"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "so this one was ranked") {
		t.Fatalf("the fixture no longer reaches the relevance tier, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, nothingIsAboutThis) {
		t.Errorf("a session holding the words asked about was disowned:\n%s", firstLines(text, 4))
	}
}

// One sentence, two surfaces: the page of sessions and the single session say
// the same thing about the same situation.
func TestBothRelevanceLeadsShareTheirSentence(t *testing.T) {
	if !strings.Contains(contextTierLead(search.TierRelevance, false), nothingIsAboutThis) {
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
