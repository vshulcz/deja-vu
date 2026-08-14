package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A matched session shows what matched and stops. Topping the second slot up
// from the start of the session put unrelated chatter directly under the
// answer — read on a real block as "two schedulers were live after the
// failover" beneath a line about the retry budget.
func TestMatchedSessionIsNotPaddedWithChatter(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "work-31", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "cron ran twice"},
			{Role: "assistant", Text: "two schedulers were live after the failover"},
			{Role: "user", Text: "which endpoint did we exempt from the retry budget?"},
			{Role: "assistant", Text: "POST /v1/payouts is exempt from the retry budget because a duplicate payout is worse than a dropped one"},
		},
	}

	got := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"endpoint", "retry", "budget"})
	if !strings.Contains(got, "/v1/payouts") {
		t.Fatalf("the answering line is missing:\n%s", got)
	}
	if strings.Contains(got, "two schedulers") {
		t.Fatalf("unrelated chatter was padded in under the answer:\n%s", got)
	}

	// Without terms nothing matched, so the opening of the session is still
	// the best summary of it and both slots are worth filling.
	plain := AutoRecallDigest([]model.Session{s}, 2000)
	if !strings.Contains(plain, "two schedulers") {
		t.Fatalf("the session-start digest lost its second line:\n%s", plain)
	}
}
