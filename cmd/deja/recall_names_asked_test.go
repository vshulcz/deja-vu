package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The counted page carried the same defect recall_context did (#2831): the
// caveat was keyed to the tier, so a page whose first session is the right one
// was disowned along with the rest (#2827).
func TestARelevancePageWhoseFirstSessionNamesTheAskedIsNotDisowned(t *testing.T) {
	dir := manySessionStore(t, 40)

	text, err := callMCPTool(dir, "recall", []byte(`{"query":"what did the quibblesnatch parser do with the stalled pipeline frames","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "so these were ranked") {
		t.Fatalf("the fixture no longer reaches the relevance tier, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, "No session is about this") {
		t.Errorf("a page led by the session that names the asked was disowned:\n%s", firstLines(text, 4))
	}
	// The count line makes the same claim, so it has to make the same one: a
	// page reading "the first one names what you asked" above "none about it"
	// is worse than either line alone.
	if strings.Contains(text, "none about it") {
		t.Errorf("the count line disagrees with the lead above it:\n%s", firstLines(text, 4))
	}
}

// And a subject the store never held keeps the caveat over its neighbours. The
// fixture is the one the recall_context control uses, for the same reason: the
// question's ordinary words must be in the store or the tier serves nothing,
// and no session may hold all of them or an earlier tier answers.
func TestARelevancePageAboutSomethingAbsentKeepsTheCaveat(t *testing.T) {
	dir := spreadWordsStore(t)

	text, err := callMCPTool(dir, "recall", []byte(`{"query":"which crystal did we pick for the antenna array tuning","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "1. [claude]") {
		t.Fatalf("nothing was served, so the caveat this test is about was never reached:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "No session is about this") {
		t.Errorf("a page about a subject the store never held was reported as named:\n%s", firstLines(text, 4))
	}
}

// spreadWordsStore builds the one shape that serves a nearest neighbour for a
// subject the store never held: the question's ordinary words are in the store,
// so the tier does not refuse, and no session holds all of them, so no earlier
// tier answers. One per session does both.
func spreadWordsStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{
		"we did pick the smaller batch size for the ingest job today",
		"the array of workers was resized after the incident",
		"tuning the retry budget took the rest of the afternoon",
	} {
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":"2026-08-0%dT10:00:00Z","sessionId":"n%d","cwd":"/work"}`, text, i+1, i) + "\n"
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("n%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
