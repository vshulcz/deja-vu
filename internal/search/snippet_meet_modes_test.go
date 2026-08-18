package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Ranking excerpts by how tightly the query's words meet only makes sense when
// the query is words. A regex is not: QueryParts splits `dad|gift` into two
// words a message can hold together without the pattern matching them that way,
// so the excerpt must still be chosen by how often the pattern actually hit.
func TestRegexSnippetsStillRankByMatchCount(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "dad gift"},
			{Role: "user", Text: "gift, gift and one more gift"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad|gift", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "one more gift") {
		t.Errorf("first snippet is %q, not the passage the pattern hit most", hits[0].Snippets[0])
	}
}

// A match found by folding scripts is a real match. Measuring how its words meet
// against the unfolded text finds nothing, which would rank a genuine hit behind
// passages the query never met in at all.
func TestFoldedMatchIsNotRankedAsWordsThatNeverMeet(t *testing.T) {
	scattered := strings.Repeat("連接池", 6) + strings.Repeat("其他內容", 200) + "修復"
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: scattered},
			{Role: "user", Text: "修復連接池洩漏的辦法"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "修复 连接池"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("a Simplified query did not reach Traditional text")
	}
	if !strings.Contains(hits[0].Snippets[0], "修復連接池洩漏") {
		t.Errorf("first snippet is %q, not the passage where the folded words meet", hits[0].Snippets[0])
	}
}
