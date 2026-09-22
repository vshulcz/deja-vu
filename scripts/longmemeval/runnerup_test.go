package main

import "testing"

// The -runner-up figure turns on one judgement: given the excerpt lines the
// recall listing prints under each candidate, did the one holding the answer
// stand out? Four outcomes, and the one that carries the finding is "blank" —
// 10 of 31 cases landed there, so counting a candidate with no answer words as
// tied with its rivals would report the listing as far more usable than it is.
func TestWhereGoldLineLandsSeparatesTheFourOutcomes(t *testing.T) {
	cand := func(answer bool, lines ...string) map[string]any {
		return map[string]any{"is_answer": answer, "snippets": lines}
	}
	unasked := map[string]bool{"fujifilm": true, "rooftop": true}

	for _, tc := range []struct {
		name  string
		cands []map[string]any
		want  string
	}{
		{"alone", []map[string]any{
			cand(true, "the fujifilm with the rooftop shot"),
			cand(false, "nothing of the sort here"),
		}, "best"},
		{"tied", []map[string]any{
			cand(true, "the fujifilm again"),
			cand(false, "a rooftop, other session"),
		}, "tied"},
		{"outscored", []map[string]any{
			cand(true, "the fujifilm again"),
			cand(false, "fujifilm on the rooftop"),
		}, "worse"},
		{"blank beats tied", []map[string]any{
			cand(true, "no answer words at all"),
			cand(false, "none here either"),
		}, "blank"},
	} {
		if got := whereGoldLineLands(tc.cands, unasked); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
