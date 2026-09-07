package stats

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// `stats --impact` said memory was credited aloud 143 times out of 6,650 and
// nothing else: the 2% folds together injections the model rightly ignored and
// injections it used without saying so, and only the second is a problem worth
// wording changes (#3079). This counts the second, from the id the reply
// carries.
func TestUsedNotCreditedCountsTheRepliesThatNameASessionAndSayNothing(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	fresh := now.Add(-24 * time.Hour)
	ss := []model.Session{{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			// Credited: counted by AgentCredits, not here.
			{Role: "assistant", Text: "déjà vu: the retry budget (claude, Aug 2, deja:abc12345) — reusing it.", Time: fresh},
			// Used and not said: the id is in the reply, the line is not.
			{Role: "assistant", Text: "the budget stays at five, from deja:abc12345", Time: fresh},
			{Role: "assistant", Text: "as in deja:99ff0011, one shard", Time: old},
			// Nothing to do with a recall.
			{Role: "assistant", Text: "running the tests now", Time: fresh},
			// A person quoting an id is not the agent crediting itself.
			{Role: "user", Text: "look at deja:abc12345", Time: fresh},
			// "deja:" with nothing that looks like a session id is prose.
			{Role: "assistant", Text: "the deja: prefix is what the block uses", Time: fresh},
		},
	}}

	total, week := UsedNotCredited(ss, now)
	if total != 2 {
		t.Fatalf("total = %d, want the two replies that named a session without the line", total)
	}
	if week != 1 {
		t.Fatalf("week = %d, want only the recent one", week)
	}
	// And the credited reply still counts as credited, in the other counter.
	if credits, _ := AgentCredits(ss, now); credits != 1 {
		t.Fatalf("credits = %d, want the one reply that said the line", credits)
	}
}
