package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "Holds every word of the query" is the strongest thing any surface says, and
// it was said over a query one of whose words had just been thrown away. The
// word forms line above it announces the drop — "quasar (ignored: no session
// matches it with the rest)" — and the strict head underneath is the AND over
// what survived, not over what was asked. Measured on this machine's store,
// eight invented subjects came back that way: a strict count of 1 to 5 and
// three or four sessions tagged as holding the whole question, every one of
// them about something the store has never held.
func TestAnAnswerMissingAWordDoesNotClaimToHoldThemAll(t *testing.T) {
	hermeticEnv(t)
	absentWordCorpus(t)

	got := mcpRecallText(t, "pgbouncer pool quasar")
	// The ladder has to have dropped it, or this asserts nothing.
	if !strings.Contains(got, "quasar") {
		t.Fatalf("the query word was not reported dropped, so this measures the wrong answer:\n%s", head(got))
	}
	if strings.Contains(got, "every word") {
		t.Errorf("a word was dropped and the answer still claims every one of them:\n%s", head(got))
	}
	if strings.Contains(got, "[holds every word of the query]") {
		t.Errorf("a session is tagged as holding a query the search could not match in full:\n%s", got)
	}
	if !strings.Contains(got, nothingIsAboutThis) {
		t.Errorf("the question named something the store does not hold and the answer never says so:\n%s", head(got))
	}
}

// And only there: the same corpus, asked without the absent word, still gets
// the strict head and the marks that say which session earned it.
func TestTheWholeQueryStillHoldsWhenEveryWordIsThere(t *testing.T) {
	hermeticEnv(t)
	absentWordCorpus(t)

	got := mcpRecallText(t, "pgbouncer pool")
	if !strings.Contains(got, "holds every word") {
		t.Errorf("an answer that does hold the whole query no longer says so:\n%s", head(got))
	}
	if !strings.Contains(got, "[holds every word of the query]") {
		t.Errorf("the matched session lost its mark:\n%s", got)
	}
	if strings.Contains(got, nothingIsAboutThis) {
		t.Errorf("an answer holding every word says nothing is about it:\n%s", head(got))
	}
}

// Two words, one of them in no session: the page shows the session that holds
// the other one, says which word it ignored, and claims nothing about it (#5711).
func TestATwoWordQueryKeepsTheWordTheStoreHolds(t *testing.T) {
	hermeticEnv(t)
	absentWordCorpus(t)

	got := mcpRecallText(t, "pgbouncer quasar")
	if !strings.Contains(got, "quasar (ignored: no session matches it with the rest)") {
		t.Fatalf("the unknown word was not reported dropped:\n%s", head(got))
	}
	if !strings.Contains(got, "held") {
		t.Errorf("the session holding the known word is not on the page:\n%s", head(got))
	}
	if !strings.Contains(got, nothingIsAboutThis) {
		t.Errorf("a page missing a query word does not say nothing is about it:\n%s", head(got))
	}
	if strings.Contains(got, "[holds every word of the query]") {
		t.Errorf("a session is tagged as holding a query it does not hold:\n%s", got)
	}
}

// The sessions such a page lists are leads the header calls nobody's answer:
// a few of them, one excerpt each, and none of the conclusion block the best
// hit of a real answer gets.
func TestATwoWordFallbackPageStaysShort(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 5 {
		at := time.Now().Add(-time.Duration(i+100) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("z%d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the zeppelin hangar door stuck again, attempt ` + strconv.Itoa(i) + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":"decided to oil the zeppelin hangar rails; that fixed it"}}`,
		})
	}
	absentWordCorpus(t)

	got := mcpRecallText(t, "zeppelin quasar")
	if !strings.Contains(got, "quasar (ignored: no session matches it with the rest)") || !strings.Contains(got, nothingIsAboutThis) {
		t.Fatalf("not the fallback page this measures:\n%s", head(got))
	}
	hits, excerpts := 0, 0
	for l := range strings.SplitSeq(got, "\n") {
		switch {
		case len(l) > 3 && l[0] >= '1' && l[0] <= '9' && strings.HasPrefix(l[1:], ". ["):
			hits, excerpts = hits+1, 0
		case hits > 0 && strings.HasPrefix(l, "- "):
			if excerpts++; excerpts > 1 {
				t.Errorf("hit %d carries more than one excerpt:\n%s", hits, got)
			}
		case hits > 0 && strings.HasPrefix(l, "  "):
			t.Errorf("a conclusions block reached a page of leads: %q", l)
		}
	}
	if hits == 0 || hits > 3 {
		t.Errorf("the page lists %d sessions, want 1 to 3:\n%s", hits, got)
	}
}

// Same shape as the #3815 corpus: more sessions than the relevance window so
// the thin match is published under the relevance label with a tail hung
// underneath it, and one session that holds both real words.
func absentWordCorpus(t *testing.T) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 55 {
		at := time.Now().Add(-time.Duration(i+2) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("f%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"routine standup chatter about the worker pool number ` + strconv.Itoa(i) + `"}}`,
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
}
