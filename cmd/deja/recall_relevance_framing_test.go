package main

import (
	"encoding/json"
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
			// Page two of an answer to a question nothing is about is still
			// not a page of matches: saying so only on page one left the same
			// contradiction one page further in.
			name: "the second page of a relevance answer", tier: search.TierRelevance, offset: 3, served: 3, total: 37,
			want: `nearest by wording to "` + q + `" (4-6 of 37 ranked, none about it)`,
		},
		{
			name: "the second page of a real answer", tier: search.TierExact, offset: 3, served: 3, total: 37,
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

// The query is echoed back, so it is bounded the way every sibling echo is.
func TestTheCountLineDoesNotEchoAWholeTranscriptBack(t *testing.T) {
	line := recallCountLine(strings.Repeat("x", 64_000), search.TierExact, 0, 3, 9)
	if len(line) > 1_000 {
		t.Errorf("the count line echoed %d bytes of query back", len(line))
	}
}

// The room kept for the lines around the hits has to cover the count line as
// it will actually be written — it carries the query, and the tool
// description asks agents to paste whole error strings. A constant that fitted
// a short query stopped covering a long one, and the line an agent navigates
// by is the one that goes first.
func TestALongQueryStillLeavesRoomForTheLineThatSaysHowToPage(t *testing.T) {
	dir := manySessionStore(t, 40)
	q := "parser rejects frames the pipeline stalled " + strings.Repeat("and the retry budget was exhausted ", 4)
	arg, _ := json.Marshal(map[string]any{"query": q, "limit": 3})
	text, err := callMCPTool(dir, "recall", arg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "call recall again with offset=") {
		t.Errorf("the line an agent pages by was trimmed off:\n%s", text)
	}
	// The count line is written whole, with the query in it: the room kept for
	// it is measured from the line itself rather than guessed at.
	want := strings.TrimRight(recallCountLine(q, search.TierRelevance, 0, 3, 41), "\n")
	if !strings.Contains(text, want) {
		t.Errorf("the count line did not survive whole:\nwant %s\ngot\n%s", want, text)
	}
	if len(text) > recallMCPBudget {
		t.Errorf("the answer is %d bytes, budget is %d", len(text), recallMCPBudget)
	}
}
