package search

import "testing"

// A question can name something specific this store has never seen — work from
// another project, another machine, another life. The word still reads as a
// term of art, and that was enough: a single match on one of the ordinary words
// around it answered with whatever session says those words most often, which
// is whichever session is longest. The subject contributed nothing.
func TestASubjectTheStoreNeverSawCarriesNothing(t *testing.T) {
	terms := []string{"oauth", "rotation", "branch", "suite", "passed"}
	// The working vocabulary is here; the subject is not.
	known := map[string]float64{"branch": 2.4, "suite": 2.1}
	if RecallWorthShowing(terms, 1, 1, known) {
		t.Error("a question about something absent was answered on one match")
	}
	// Two ordinary words that met in one message is a different rule, and this
	// one leaves it alone.
	if !RecallWorthShowing(terms, 2, 1, known) {
		t.Error("the two-word rule was lost")
	}
	// Whether anything else in the question carries on its own stays the shape
	// rule's business. "passed" is not on the working-word list and is long
	// enough to pass it, so a question holding it still fires — which is why
	// this removes one of the benchmark's three false fires and not all three.
	if !RecallWorthShowing(terms, 1, 1, map[string]float64{"branch": 2.4, "suite": 2.1, "passed": 2.0}) {
		t.Error("a known word of identifier shape stopped carrying")
	}
}

// The subject is present: one match on it is an answer, and that is the rule
// this must not weaken.
func TestAKnownSubjectStillCarriesOnItsOwn(t *testing.T) {
	terms := []string{"pgbouncer", "here"}
	known := map[string]float64{"pgbouncer": 4.2, "here": 1.0}
	if !RecallWorthShowing(terms, 1, 1, known) {
		t.Error("a rare word the store holds stopped carrying a match")
	}
}

// A short question is all subject. "как там pr, смержился?" names the thing and
// one working verb: there is nothing else in it a candidate could have matched
// instead, so it is judged on shape as before and the store is never asked.
func TestAShortQuestionIsJudgedOnShapeAlone(t *testing.T) {
	terms := []string{"смержился", "pr"}
	if !RecallWorthShowing(terms, 1, 1, map[string]float64{"pr": 3.0}) {
		t.Error("a short question naming its subject fell silent")
	}
}
