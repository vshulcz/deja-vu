package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// coverageStore holds forty sessions of filler an agent appends to a query
// ("final verification") and three that each say one specific thing.
func coverageStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, ts, text string) {
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q,"sessionId":%q,"cwd":"/work"}`, text, ts, id) + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		write(fmt.Sprintf("f%d", i), fmt.Sprintf("2026-08-02T10:00:%02dZ", i), fmt.Sprintf("pipeline run %d passed final verification", i))
	}
	write("rule", "2026-08-03T10:00:00Z", "rule for this repo: every edit to a page in docs/guide.md must be followed by scripts/genfeed.sh so the feed stays fresh")
	write("zebra", "2026-08-03T11:00:00Z", "the zebra cache was tuned for the ingest job")
	write("other", "2026-08-03T12:00:00Z", "the ingest job retries were capped at three")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The shape agents send: a path, the task's words and filler, one of them a
// word nothing holds. The session that holds the path and most of the rest
// is an answer in part, and the page used to say nothing was about it.
func TestALongQueryMostlyHeldIsNotDisowned(t *testing.T) {
	dir := coverageStore(t)
	text, _, _, _, err := recallTextResult(dir, "docs/guide.md quuxify typo edit feed page final verification", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "· rule ·") {
		t.Fatalf("the session holding the rule was not served:\n%s", firstLines(text, 8))
	}
	if strings.Contains(text, "No session is about this") || strings.Contains(text, "none about it") {
		t.Errorf("a page led by a session holding most of the query was disowned:\n%s", firstLines(text, 5))
	}
	if !strings.Contains(text, "No session matched every word; session 1 holds") || !strings.Contains(text, "docs/guide.md") {
		t.Errorf("the lead does not say what the session holds:\n%s", firstLines(text, 5))
	}
	if !strings.Contains(text, "quuxify") {
		t.Errorf("the lead does not name the word nothing holds:\n%s", firstLines(text, 5))
	}
}

// Filler is not an answer: forty sessions hold "final verification", none
// holds the path or what it is about.
func TestAQueryWhoseSubjectIsAbsentKeepsTheCaveat(t *testing.T) {
	dir := coverageStore(t)
	for _, q := range []string{
		"internal/auth/oauth.go token rotation pipeline final verification",
		// The words are held and rare, but not the file the query names.
		"docs/missing.md zebra cache tuned",
	} {
		text, _, _, _, err := recallTextResult(dir, q, "", 5, 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "No session matched every word; session") {
			t.Errorf("%q: a page without its subject was read as about it:\n%s", q, firstLines(text, 5))
		}
	}
}

func TestCoverageNeedsHalfTheQueryAndARareWord(t *testing.T) {
	idf := map[string]float64{"docs/guide.md": 4, "feed": 4, "final": 0.5, "verification": 0.5}
	for _, tc := range []struct {
		held, terms []string
		want        bool
	}{
		{[]string{"docs/guide.md", "feed"}, []string{"docs/guide.md", "feed", "final", "verification"}, true},
		{[]string{"final", "verification"}, []string{"docs/guide.md", "feed", "final", "verification"}, false},
		{[]string{"docs/guide.md"}, []string{"docs/guide.md", "feed"}, false},
		{[]string{"docs/guide.md", "feed"}, []string{"docs/guide.md", "feed", "quasar"}, false},
		{[]string{"feed", "final"}, []string{"docs/guide.md", "feed", "final", "verification"}, false},
		{[]string{"feed", "final"}, []string{"feed", "final", "x", "y", "z"}, false},
	} {
		c := recallCoverage{at: 0, held: tc.held, terms: tc.terms, idf: idf}
		for _, t := range tc.held {
			c.weight += idf[t]
		}
		if got := c.aboutIt(); got != tc.want {
			t.Errorf("held %v of %v: aboutIt = %v, want %v", tc.held, tc.terms, got, tc.want)
		}
	}
}
