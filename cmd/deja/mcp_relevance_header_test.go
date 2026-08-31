package main

import (
	"strings"
	"testing"
)

// One line says no session is about this; the next used to open "deja recall
// for <query>" over three sessions, and with an offset it called them matches
// outright. The caveat and the header cannot both be true, and the header is
// the one an agent reads as the answer's title (#2074).
func TestTheRelevanceHeaderDoesNotClaimTheQuery(t *testing.T) {
	dir := manySessionStore(t, 40)
	const q = "parser rejects frames the pipeline stalled"

	for _, offset := range []int{0, 1} {
		text, _, _, _, err := recallTextResult(dir, q, "", 3, offset, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "nearest by wording") {
			t.Fatalf("the fixture no longer reaches the relevance tier, so this guards nothing:\n%s", firstLines(text, 3))
		}
		header := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "deja recall") {
				header = l
				break
			}
		}
		if header == "" {
			t.Fatalf("offset %d: no header line at all:\n%s", offset, firstLines(text, 4))
		}
		if strings.Contains(header, "recall for") {
			t.Errorf("offset %d: the header calls the sessions the query's own: %q", offset, header)
		}
		if strings.Contains(header, "match") {
			t.Errorf("offset %d: the header calls them matches: %q", offset, header)
		}
		if !strings.Contains(header, "ranked") {
			t.Errorf("offset %d: the header does not say what the number is: %q", offset, header)
		}
		// Paging is arithmetic the agent navigates by: it asks for
		// offset=served next, so where it already is has to be on the line.
		if offset > 0 && !strings.Contains(header, "2-") {
			t.Errorf("offset %d: the header lost where the page starts: %q", offset, header)
		}
	}
}
