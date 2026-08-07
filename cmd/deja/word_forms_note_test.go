package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja retry` answers from the exact tier and stops there, so the sessions
// that wrote "retries" are neither returned nor mentioned — and to whoever
// reads the output, one form's sessions look like the whole store. deja
// narrates every other rung of the ladder; this one was silent.
func TestSearchNamesTheWordFormsItLeftOut(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for id, text := range map[string]string{
		"wfa": "we decided the uploader will retry once on 5xx",
		"wfb": "we decided to cap uploader retries at 3",
	} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "wf", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	result, err := index.Search(dir, query.Options{Query: "retry", All: true})
	if err != nil {
		t.Fatal(err)
	}
	o := query.Options{Query: "retry", All: true, Tier: search.TierExact}
	detailed, err := search.RunDetailed(result, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed.Hits) != 1 || detailed.Hits[0].Session.ID != "wfa" {
		t.Fatalf("exact tier hits = %+v, want only wfa", detailed.Hits)
	}
	note := otherWordFormsNote(dir, o, detailed.Hits)
	if !strings.Contains(note, `"retries" in 1 more`) {
		t.Fatalf("note = %q, want it to name the retries session the answer leaves out", note)
	}

	// A form already on the page is not left out, and a query with no other
	// form in the corpus gets no line at all.
	uploader := query.Options{Query: "uploader", All: true, Tier: search.TierExact}
	ss, err := index.Search(dir, uploader)
	if err != nil {
		t.Fatal(err)
	}
	both, err := search.RunDetailed(ss, uploader)
	if err != nil {
		t.Fatal(err)
	}
	if got := otherWordFormsNote(dir, uploader, both.Hits); got != "" {
		t.Errorf("uploader note = %q, want silence", got)
	}
	// Nothing to leave out when nothing came back, and the close/relevance
	// tiers already narrate their own variants.
	if got := otherWordFormsNote(dir, o, nil); got != "" {
		t.Errorf("empty result note = %q, want silence", got)
	}
	closeTier := o
	closeTier.Tier = search.TierClose
	if got := otherWordFormsNote(dir, closeTier, detailed.Hits); got != "" {
		t.Errorf("close tier note = %q, want silence", got)
	}
}

// "retry" inside "retrying" is a different form; counting it as present would
// hide the one the note exists to name.
func TestContainsWordIsNotSubstring(t *testing.T) {
	for _, tc := range []struct {
		text, word string
		want       bool
	}{
		{"the uploader is retrying the chunk", "retry", false},
		{"the uploader will retry once", "retry", true},
		{"retry.", "retry", true},
		{"cap uploader retries at 3", "retries", true},
	} {
		if got := containsWord(tc.text, tc.word); got != tc.want {
			t.Errorf("containsWord(%q, %q) = %v", tc.text, tc.word, got)
		}
	}
}
