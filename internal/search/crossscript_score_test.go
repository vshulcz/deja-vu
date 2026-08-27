package search

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Postings are keyed on folded CJK, so a Simplified query legitimately reaches
// a Traditional record and the match is counted. Scoring then ran on the
// surface text, where the query's words appear nowhere: term frequency stayed
// zero and the record scored zero, ranking below every real match (#1605).
//
// Stated as the invariant rather than as an ordering, because ordering also
// depends on length normalisation and would pass for the wrong reason: the
// same session, same words, same length, must score the same whichever script
// it is written in.
func TestScriptDoesNotChangeTheScore(t *testing.T) {
	now := time.Now()
	same := func(id, a, b string) model.Session {
		return model.Session{
			ID: id, Harness: "claude", Project: "app", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: a},
				{Role: "assistant", Text: b},
			},
		}
	}
	sessions := []model.Session{
		same("simp", "调度器 又坏了，昨天也是", "调度器 的重试设定改过了"),
		same("trad", "調度器 又壞了，昨天也是", "調度器 的重試設定改過了"),
	}
	// Sessions that never say it: with the word in every document its IDF is
	// zero and this would measure nothing.
	for i := 0; i < 8; i++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("f%d", i), Harness: "claude", Project: "app", Updated: now,
			Messages: []model.Message{{Role: "user", Text: "今天的部署很順利，沒有問題"}},
		})
	}

	hits, err := runScored(sessions, Options{Query: "调度器", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if len(hits) != 2 {
		t.Fatalf("both scripts should match through the fold, got %d: %v", len(hits), byID)
	}
	if byID["trad"].Score <= 0 {
		t.Fatalf("the cross-script hit scored %v, so it ranks below every real match",
			byID["trad"].Score)
	}
	if d := math.Abs(byID["trad"].Score - byID["simp"].Score); d > 1e-9 {
		t.Errorf("the same session scores differently by script: trad=%v simp=%v",
			byID["trad"].Score, byID["simp"].Score)
	}
	if byID["trad"].Count != byID["simp"].Count {
		t.Errorf("counts differ by script: trad=%d simp=%d",
			byID["trad"].Count, byID["simp"].Count)
	}
}
