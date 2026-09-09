package search

import "testing"

// A question of three terms or fewer skipped the store check entirely — "a
// short question is all subject" — so `decide saltmarsh` asked where saltmarsh
// has never been said fired on `decide`. The ranking knows which of the
// question's words a session actually matched; the gate now asks (#3351).
func TestAShortQuestionNeedsTheSubjectToHaveMatched(t *testing.T) {
	terms := []string{"decide", "saltmarsh"}

	if RecallWorthShowingNaming(terms, 1, 0, 0, nil) {
		t.Error("fired on the working word beside the subject")
	}
	if !RecallWorthShowingNaming(terms, 1, 0, 1, nil) {
		t.Error("the session that matched the subject was refused")
	}
	// Nothing matched at all is still nothing.
	if RecallWorthShowingNaming(terms, 0, 0, 1, nil) {
		t.Error("fired on a session that matched none of the question")
	}
	// A caller with no count — the bridged retry ranks on neighbouring terms,
	// so counting the question's own words there would be a claim about a
	// query nobody asked — keeps the older rule.
	if !RecallWorthShowingNaming(terms, 1, 0, -1, nil) {
		t.Error("a ranking that cannot answer was treated as a refusal")
	}
	// And a question naming nothing is unchanged: two ordinary words earn it,
	// one does not.
	plain := []string{"build", "retry"}
	if RecallWorthShowingNaming(plain, 1, 0, 0, nil) {
		t.Error("one ordinary word fired")
	}
	if !RecallWorthShowingNaming(plain, 2, 0, 0, nil) {
		t.Error("two ordinary words did not")
	}
}
