package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The first snippet is the line a reader — or an agent with one line of budget —
// actually reads. A message repeating one query word beat the message that said
// both words together, so the excerpt showed noise and the answer came second.
func TestSnippetLeadsWithThePassageWhereWordsMeet(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: strings.Repeat("gift ", 6) + strings.Repeat("filler ", 300) + "dad"},
			{Role: "user", Text: "the gift my dad gave me for the trip"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad gift"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "my dad gave me") {
		t.Errorf("first snippet is %q, not the passage where the words meet", hits[0].Snippets[0])
	}
}

// Among passages that are equally tight, the one carrying more of the query
// still leads — otherwise a fix could pass the test above by ranking on
// tightness alone and losing the signal count was there for.
func TestSnippetStillPrefersTheDenserOfTwoTightPassages(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "dad gift"},
			{Role: "user", Text: "dad gift, and another dad gift"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad gift"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "another dad gift") {
		t.Errorf("first snippet is %q, not the passage carrying more of the query", hits[0].Snippets[0])
	}
}
