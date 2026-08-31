package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// When a query's subject is a word no session holds, the stemmed tier answers
// on what is left. The counted page says which words it threw away — "ignored:
// no session matches it with the rest" — and recall_context, which returns a
// whole session, printed only `[stemmed]`. So the tool that hands an agent the
// most text was the one that did not say the question's subject had been
// dropped, which is the failure #2074 is about (#2827).
func TestTheContextDigestNamesTheWordsItIgnored(t *testing.T) {
	dir := manySessionStore(t, 40)

	// "quibblesnatch" is in the store, "wombatron" is in no session, so the
	// tier answers on the rest and drops the invented word.
	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"the wombatron quibblesnatch parser rejects frames"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to caveat:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "wombatron") {
		t.Errorf("the word the search dropped is not named:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "ignored") {
		t.Errorf("the digest does not say the word was ignored:\n%s", firstLines(text, 4))
	}
}

// And a query every word of which the store holds says nothing of the sort.
func TestTheContextDigestIsQuietWhenNothingWasIgnored(t *testing.T) {
	dir := manySessionStore(t, 40)
	if _, err := index.AllMeta(dir); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "recall_context", []byte(`{"query":"quibblesnatch parser"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "ignored") {
		t.Errorf("nothing was dropped and the digest said otherwise:\n%s", firstLines(text, 4))
	}
}
