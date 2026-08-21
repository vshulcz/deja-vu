package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An answer often takes one sentence to set up and the next to land, so the
// quote carries two lines past the match rather than one.
func TestQuoteCarriesTwoLinesAfterTheMatch(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "two", Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer"},
			{Role: "assistant", Text: "смотрел pgbouncer\nпробовали и 20, и 60\nостановились на 40 коннектах\nдальше не трогали"},
			{Role: "user", Text: "ок"},
			{Role: "assistant", Text: "ага"},
		},
	}
	got := AutoRecallDigestForAsked([]model.Session{s}, 4096,
		[]string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "остановились на 40 коннектах") {
		t.Errorf("the quote stopped one line short of the answer:\n%s", got)
	}
	// And it stops there. Every further line costs room another session would
	// have taken, which on a real store is what a third line gives up.
	if strings.Contains(got, "дальше не трогали") {
		t.Errorf("the quote ran past the answer into the next subject:\n%s", got)
	}
}
