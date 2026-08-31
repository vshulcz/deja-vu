package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The line that introduces the sessions said "deja recall for <the query>"
// whatever the tier — so an answer whose first line says no session is about
// this went on to head the payload with the question itself. Measured live,
// that is what one invented answer was built on: plausible sessions about the
// wrong thing, under a heading that read as an answer (#2074).
func TestTheCountLineSaysWhatFollowsIt(t *testing.T) {
	const q = "why did the quokka telemetry sharding keep failing"
	for _, c := range []struct {
		name           string
		tier           string
		offset, served int
		total          int
		want           string
	}{
		{
			name: "nothing is about it", tier: search.TierRelevance, served: 3, total: 37,
			want: `nearest by wording to "` + q + `" (3 of 37 ranked, none about it)`,
		},
		{
			name: "nothing is about it, all of them shown", tier: search.TierRelevance, served: 2, total: 2,
			want: `nearest by wording to "` + q + `" (2 ranked, none about it)`,
		},
		{
			name: "a real match", tier: search.TierExact, served: 3, total: 37,
			want: `deja recall for "` + q + `" (3 of 37 matched)`,
		},
		{
			name: "a real match, all of them shown", tier: search.TierExact, served: 2, total: 2,
			want: `deja recall for "` + q + `" (2 match(es))`,
		},
		{
			// A page of a longer answer says where it is, and the tier line
			// above it has already said what these are.
			name: "the second page", tier: search.TierRelevance, offset: 3, served: 3, total: 37,
			want: `deja recall for "` + q + `" (matches 4-6 of 37)`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := strings.TrimRight(recallCountLine(q, c.tier, c.offset, c.served, c.total), "\n")
			if got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

// And the two halves of a relevance answer agree: the tier line says no
// session is about this, and the line above the payload does not then head it
// with the question.
func TestBothLinesOfARelevanceAnswerSayTheSameThing(t *testing.T) {
	line := recallCountLine("anything at all", search.TierRelevance, 0, 3, 9)
	if strings.Contains(line, "deja recall for") {
		t.Errorf("the payload is headed by the question it does not answer: %s", line)
	}
	if !strings.Contains(line, "none about it") {
		t.Errorf("the line does not say what the sessions are: %s", line)
	}
}
