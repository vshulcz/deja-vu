package main

import (
	"strings"
	"testing"
)

// "No session is about this" was printed whenever the relevance tier answered,
// which on any store with competition is most questions — including the ones
// whose answer is served at rank 1. Measured on a real store, 20 of 20
// questions lifted verbatim out of indexed sessions were disowned that way, and
// an agent told the right answer is about nothing learns to ignore the line —
// which costs what #2074 bought with it (#2827).
func TestARelevanceAnswerThatNamesTheAskedIsNotDisowned(t *testing.T) {
	dir := manySessionStore(t, 40)

	// Reaches the relevance tier — no session holds every word — and the
	// session it serves is the one that says "quibblesnatch".
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"what did the quibblesnatch parser do with the stalled pipeline frames"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "so this one was ranked") {
		t.Fatalf("the fixture no longer reaches the relevance tier, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, "No session is about this") {
		t.Errorf("a session that names what was asked was disowned:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "rare") {
		t.Errorf("the session holding the asked-about word was not the one served:\n%s", firstLines(text, 6))
	}
}

// And the case the line exists for keeps it: a subject this store has never
// held, served its nearest neighbour, under the caveat.
//
// The fixture is built for that and not borrowed: the question's ordinary words
// have to be in the store — otherwise the tier refuses and serves nothing,
// which is the other right answer and not this case — and no session may hold
// all of them, or an earlier tier answers and this code is never reached. So
// they go one per session.
func TestARelevanceAnswerAboutSomethingAbsentKeepsTheCaveat(t *testing.T) {
	dir := spreadWordsStore(t)

	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"which crystal did we pick for the antenna array tuning"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so the caveat this test is about was never reached:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "No session is about this") {
		t.Errorf("a subject the store never held was reported as named:\n%s", firstLines(text, 4))
	}
}
