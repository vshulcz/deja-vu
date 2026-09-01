package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// On a machine with no history yet, recall answered "No prior deja sessions
// matched <query>" — which reads as "your query missed" and invites an agent to
// keep rephrasing. The same function already separates three other reasons an
// answer is empty, for the reason its own comment gives: an agent told nothing
// matched concludes the work was never done (#680). "Nothing is indexed" is the
// fourth, and it is the one a first run always hits.
func TestAnEmptyStoreSaysSoRatherThanBlamingTheQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("the fixture indexed %d sessions, so this is not the empty case", len(metas))
	}

	for _, tool := range []string{"recall", "recall_context"} {
		text, err := callMCPTool(dir, tool, json.RawMessage(`{"query":"how did we set up the deploy"}`))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "matched") && !strings.Contains(text, "no indexed history") {
			t.Errorf("%s: an empty store answers as though the query missed:\n%s", tool, firstLines(text, 3))
		}
		if !strings.Contains(text, "no indexed history yet") {
			t.Errorf("%s: does not say the store is empty:\n%s", tool, firstLines(text, 3))
		}
		// A store nobody has forgotten anything from is a first run, and must
		// not be described as a removal — the two sentences share a prefix, so
		// the assertion above cannot tell them apart on its own (#2862).
		if strings.Contains(text, "forgotten") {
			t.Errorf("%s: a first run was reported as a deliberate removal:\n%s", tool, firstLines(text, 3))
		}
	}
}

// And a store that holds something keeps the ordinary answer: "nothing is
// indexed" must not be said about a query that simply found nothing.
func TestAStoreWithHistoryStillBlamesNeitherSide(t *testing.T) {
	dir := manySessionStore(t, 3)
	text, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"zzzqqq wwwxxx vvvbbb"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "no indexed history") {
		t.Errorf("a store with sessions in it was called empty:\n%s", firstLines(text, 3))
	}
}
