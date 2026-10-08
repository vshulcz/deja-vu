package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A JSON dump in tool output holds every word of the query, so it won the
// excerpt slots of a session that was mostly dumps, and recall quoted
// `},\n {\n "type": "tool"` under it (#4780). What someone said comes first.
func TestExcerptsPreferSpeechOverToolOutputDumps(t *testing.T) {
	now := time.Now().UTC()
	// Rows the way sqlite3 prints a JSON column: long values, few keys, so the
	// key density alone does not call it data.
	var rows strings.Builder
	for i := range 6 {
		fmt.Fprintf(&rows, "2026-05-24 20:02:%02d|{\"type\":\"tool\",\"tool\":\"bash\",\"state\":{\"output\":\"%s the reviewer asked the owner about the pgx upgrade\"}}\n",
			i, strings.Repeat("ordinary build output line ", 12))
	}
	s := model.Session{
		Harness: "opencode", ID: "s", Project: "p", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: rows.String(), Time: now},
			{Role: "assistant", Text: "The pgx upgrade goes to Dana: she is the owner and the reviewer.", Time: now},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "pgx upgrade reviewer owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(hits[0].Snippets) == 0 {
		t.Fatalf("no excerpt: %+v", hits)
	}
	if first := hits[0].Snippets[0]; !strings.Contains(first, "Dana") {
		t.Errorf("exact tier: first excerpt is the dump, not what was said: %q", first)
	}
	// The relevance tier picks its excerpts on its own.
	rel := RelevanceHitsWeighted([]model.Session{s}, []string{"pgx", "upgrade", "reviewer", "owner"}, nil)
	if len(rel) != 1 || len(rel[0].Snippets) == 0 {
		t.Fatalf("relevance tier: no excerpt: %+v", rel)
	}
	if first := rel[0].Snippets[0]; !strings.Contains(first, "Dana") {
		t.Errorf("relevance tier: first excerpt is the dump, not what was said: %q", first)
	}
}
