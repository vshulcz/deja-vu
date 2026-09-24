package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func aboutGateHit(text string) search.Hit {
	return search.Hit{Session: model.Session{
		Harness:  "claude",
		ID:       "s1",
		Messages: []model.Message{{Role: "assistant", Text: text}},
	}}
}

// An agent writes its own question, and its own question carries words the
// session that answers it never used. The gate asked whether one session speaks
// every word of the query, so a session holding the answer verbatim was judged
// unrelated for not saying "failing" or "command" — and the sentence above it
// tells the model the answer is not an answer.
//
// The words that decide are the ones that identify the subject: the rarest
// leadTermsKept of them, which is what the ranking already judges a session on
// and what the comment above this gate always claimed it did.
func TestTheAboutGateJudgesTheIdentifyingWordsNotTheFiller(t *testing.T) {
	terms := []string{"svc_fixtures", "make", "test", "failing", "command"}
	idf := map[string]float64{
		"svc_fixtures": 6.1, // named once in the store
		"make":         1.4,
		"test":         1.1,
		"failing":      0.6, // in forty other sessions
		"command":      0.5,
	}

	answers := aboutGateHit("the suite reads its fixture directory from SVC_FIXTURES: run SVC_FIXTURES=$PWD/fixtures make test and it passes")
	if !relevanceHitsAreAboutIt([]search.Hit{answers}, terms, idf) {
		t.Error("a session that speaks the question's identifying words reads as unrelated")
	}

	// The other half of the same sentence, from #2074: a hit that carries only
	// the ordinary words is not about the question, and the warning it earned
	// has to survive.
	elsewhere := aboutGateHit("the deploy command kept failing on shard 7 in this repository")
	if relevanceHitsAreAboutIt([]search.Hit{elsewhere}, terms, idf) {
		t.Error("a session naming nothing the question identifies reads as about it")
	}

	// A store too small for idf to separate anything collapses every ratio to
	// zero, and then no word is identifying: ordering by shape makes the lead
	// whichever word is longest. Loosening the rule there let a page about a
	// subject the store had never held read as an answer, on one ordinary word
	// it happened to share — #2074 exactly. So on such a store the old rule
	// stands: every word of the question, or the caveat.
	flat := map[string]float64{"svc_fixtures": 0, "make": 0, "test": 0, "failing": 0, "command": 0}
	if relevanceHitsAreAboutIt([]search.Hit{answers}, terms, flat) {
		t.Error("on a store idf cannot separate, a partial match read as about the question")
	}
	if !relevanceHitsAreAboutIt([]search.Hit{aboutGateHit("svc_fixtures make test failing command all in one session")}, terms, flat) {
		t.Error("on such a store a session holding every word still reads as unrelated")
	}
}
