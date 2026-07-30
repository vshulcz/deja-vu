package search

import "testing"

func TestIsSmokeTestKeepsRealSessions(t *testing.T) {
	if !isSmokeTest("Reply with the single word OK.", []string{"OK"}) {
		t.Error("a token asked for and a token returned is a harness check")
	}
	if !isSmokeTest("скажи ок", []string{"Ок!"}) {
		t.Error("same in Russian")
	}
	// A short but real exchange is memory worth having.
	if isSmokeTest("why does the pool exhaust under load?", []string{"pgx caches statements per connection"}) {
		t.Error("a real question with a real answer must survive")
	}
	// A question with no answer recorded is still what someone asked.
	if isSmokeTest("the original digest content marker_alpha", nil) {
		t.Error("no reply is not the same as a token reply")
	}
	// The prompt shape alone is not enough: a real answer means real work.
	if isSmokeTest("reply with your assessment of the retry budget", []string{
		"the budget is wrong: it retries four times inside a one second deadline"}) {
		t.Error("a substantial answer means this was not a smoke test")
	}
}
