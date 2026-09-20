package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The agent-facing surface is where the label costs the most: "No session is
// about this" over a session that holds every word of the question teaches an
// agent to ignore the line, and on a store big enough for the relevance tail
// that is what a thin match arrives as (#3815).
func TestRecallDoesNotSayNothingMatchedWhenSomethingDid(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// More sessions than the window the tail is drawn from, or the strict
	// answer is served on its own tier and none of this happens.
	for i := 0; i < 55; i++ {
		at := time.Now().Add(-time.Duration(i+2) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("f%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"routine standup chatter about the worker pool number ` + fmt.Sprint(i) + `"}}`,
		})
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "held.jsonl"), "held", []string{
		`{"type":"user","sessionId":"held","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pgbouncer pool kept dropping connections at peak"}}`,
		`{"type":"assistant","sessionId":"held","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":"we moved pgbouncer to transaction pooling and it held"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// One session holds both words; the filler holds one of them, which is
	// what gives the ranking something to hang underneath the match.
	got := mcpRecallText(t, "pgbouncer pool")
	if strings.Contains(got, nothingIsAboutThis) {
		t.Fatalf("the answer holds every word of the query and says it holds nothing:\n%s", head(got))
	}
	if !strings.Contains(got, "1 session below holds every word") {
		t.Fatalf("the agent is not told that part of this answer matched:\n%s", head(got))
	}
	// And which session it was: the order is the merged ranking's, so a
	// count alone does not say.
	if !strings.Contains(got, "[holds every word of the query]") {
		t.Fatalf("the matched session is not marked among the ranked ones:\n%s", got)
	}
	if !strings.Contains(got, "1 holds every word") {
		t.Fatalf("the count line still reads as a set of guesses:\n%s", head(got))
	}
}

// The relevance label means nothing matched. It is also what a strict answer
// of fewer than ten sessions is published as, and there every sentence built
// on the label alone is false (#3815).
func TestTheRelevanceLeadSaysHowMuchOfTheAnswerMatched(t *testing.T) {
	if got := relevanceLead(0); !strings.Contains(got, "no exact match") {
		t.Fatalf("an answer that matched nothing says: %q", got)
	}
	one := relevanceLead(1)
	if !strings.Contains(one, "1 session holds every word") {
		t.Fatalf("one strict session: %q", one)
	}
	if strings.Contains(one, "no exact match") {
		t.Fatalf("the line disowns a match it is reporting: %q", one)
	}
	three := relevanceLead(3)
	if !strings.Contains(three, "3 sessions hold every word") {
		t.Fatalf("three strict sessions: %q", three)
	}
}

func strictResult(ids ...string) index.SearchResult {
	r := index.SearchResult{Tier: search.TierRelevance, StrictIDs: map[string]bool{}}
	for _, id := range ids {
		r.StrictIDs["claude:"+id] = true
	}
	r.Strict = len(r.StrictIDs)
	return r
}

func hitsFor(ids ...string) []search.Hit {
	hits := make([]search.Hit, 0, len(ids))
	for _, id := range ids {
		hits = append(hits, search.Hit{
			Session: model.Session{ID: id, Harness: "claude"},
			Tier:    search.TierRelevance,
		})
	}
	return hits
}

// Which hits matched is not readable from their order: the merged ranking can
// put a ranked session above a strict one, so the flag travels on the hit.
func TestStrictHitsAreFlaggedAndTheRankedTailIsNot(t *testing.T) {
	hits := markStrictHits(hitsFor("ranked-first", "matched"), strictResult("matched"))
	if hits[0].Strict {
		t.Fatal("a session the ranking brought in is flagged as a match")
	}
	if !hits[1].Strict {
		t.Fatal("the session that holds every query word is not flagged")
	}
	// An ordinary relevance answer has no head, and nothing to say about it.
	plain := markStrictHits(hitsFor("a", "b"), index.SearchResult{Tier: search.TierRelevance})
	for _, h := range plain {
		if h.Strict {
			t.Fatalf("%q flagged on an answer with no strict head", h.Session.ID)
		}
	}
}

func TestTheRecallCountLineSaysHowManyHoldEveryWord(t *testing.T) {
	line := recallCountLine("retry budget", search.TierRelevance, 0, 3, 41, 2, false)
	if !strings.Contains(line, "2 hold every word") {
		t.Fatalf("count line: %q", line)
	}
	if strings.Contains(line, "none about it") {
		t.Fatalf("the count line disowns two sessions that hold the query: %q", line)
	}
	if !strings.Contains(line, "3 of 41") {
		t.Fatalf("the page arithmetic an agent pages with is gone: %q", line)
	}
	paged := recallCountLine("retry budget", search.TierRelevance, 3, 3, 41, 1, false)
	if !strings.Contains(paged, "4-6 of 41") || !strings.Contains(paged, "1 holds every word") {
		t.Fatalf("page two: %q", paged)
	}
	// Nothing matched: the line that says so is still the right one.
	none := recallCountLine("retry budget", search.TierRelevance, 0, 3, 41, 0, false)
	if !strings.Contains(none, "none about it") {
		t.Fatalf("an answer that matched nothing says: %q", none)
	}
}

func TestTheContextLeadDoesNotDisownASessionThatHoldsTheQuery(t *testing.T) {
	lead := contextTierLead(search.TierRelevance, true, false)
	if strings.Contains(lead, nothingIsAboutThis) {
		t.Fatalf("the session below holds every query word: %q", lead)
	}
	if !strings.Contains(lead, "holds every word of the query") {
		t.Fatalf("lead: %q", lead)
	}
	// The strict fact outranks the wording heuristic, which reads the same
	// session and can only guess at what this knows.
	if contextTierLead(search.TierRelevance, true, true) != lead {
		t.Fatal("the wording heuristic overrode the fact about retrieval")
	}
}

// The prompt promises a question the store answers. A relevance answer with no
// head is not one; the same answer with a head is, and it is the head that
// gets counted and dated (#3815).
func TestTheInstallPromptKeepsTheStrictHeadAndDropsTheTail(t *testing.T) {
	sessions := []model.Session{
		{ID: "ranked", Harness: "claude"},
		{ID: "matched", Harness: "claude"},
		{ID: "matched-too", Harness: "claude"},
	}
	kept := matchedSessions(strictResult("matched", "matched-too"), sessions)
	if len(kept) != 2 || kept[0].ID != "matched" || kept[1].ID != "matched-too" {
		t.Fatalf("kept %v", kept)
	}
	if got := matchedSessions(index.SearchResult{Tier: search.TierRelevance}, sessions); len(got) != 0 {
		t.Fatalf("an answer that matched nothing was accepted: %v", got)
	}
	if got := matchedSessions(index.SearchResult{Tier: search.TierExact}, sessions); len(got) != 3 {
		t.Fatalf("an exact answer lost sessions: %v", got)
	}
}
