package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Postings are keyed on folded CJK, so a query in one script reaches a record
// in the other — and the scorer counted on the unfolded pair, so BM25 saw a
// term frequency of zero for a match the postings had already found. The
// cross-script session counted twice and scored nothing, ranking below one that
// matched once (#1605).
func TestACrossScriptMatchScoresLikeAnyOther(t *testing.T) {
	now := time.Now()
	traditional := model.Session{
		Harness: "claude", Project: "app", ID: "trad", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "調度器為什麼會重複執行任務"},
			{Role: "user", Text: "調度器又壞了"},
		},
	}
	simplified := model.Session{
		Harness: "claude", Project: "app", ID: "simp", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "调度器为什么会重复执行任务"},
			{Role: "user", Text: "我们把重试次数限制为三次"},
		},
	}
	// Both directions: whichever side has to be folded is the one that used to
	// score zero.
	for _, q := range []string{"调度器", "調度器"} {
		hits, err := runScored([]model.Session{traditional, simplified}, Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 {
			t.Fatalf("%s: %d hits, want both sessions — this measures nothing otherwise", q, len(hits))
		}
		for _, h := range hits {
			if h.Score == 0 {
				t.Errorf("%s: %s counted %d and scored zero", q, h.Session.ID, h.Count)
			}
		}
		// The session that matched twice leads, whichever script the query is
		// written in.
		if hits[0].Session.ID != "trad" {
			t.Errorf("%s: %s leads with count=%d over trad with two matches",
				q, hits[0].Session.ID, hits[0].Count)
		}
	}
}
