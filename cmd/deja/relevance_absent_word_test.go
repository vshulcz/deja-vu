package main

import (
	"fmt"
	"os"
	"path/filepath"
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

// Same shape as the #3815 corpus: more sessions than the relevance window so
// the thin match is published under the relevance label with a tail hung
// underneath it, and one session that holds both real words.
func absentWordCorpus(t *testing.T) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
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
}
