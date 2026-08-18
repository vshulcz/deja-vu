package search

import (
	"strings"
	"testing"
)

// #1329 stopped blame quoting the first message to name a file rather than the
// one that says the most about it. Inside that message the excerpt still began
// at the first occurrence, so a session that noticed a file in passing and took
// it apart four paragraphs down was excerpted on the passing line — the part a
// reader judges the hit by, showing none of the discussion.
func TestTheExcerptLandsWhereTheTermIsDiscussed(t *testing.T) {
	passing := "we started from the deploy checklist and noticed api.go in the diff, "
	filler := strings.Repeat("then we went through the migration plan for the billing tables, ", 6)
	dense := "api.go parses the webhook body, api.go validates the signature, and api.go is where the retry lives"

	got := snippet(passing+filler+dense, "api.go", nil)
	if !strings.Contains(got, "parses the webhook body") {
		t.Errorf("the excerpt shows the passing mention rather than the discussion:\n%s", got)
	}
}

// One mention is where the excerpt goes, since there is nothing to compare it
// against — and a term at the very top must not drag the window backwards.
func TestASingleMentionIsStillTheCentre(t *testing.T) {
	msg := "api.go is where the retry lives, " + strings.Repeat("and the rest of this message is about the billing tables, ", 8)
	got := snippet(msg, "api.go", nil)
	if !strings.Contains(got, "api.go") {
		t.Errorf("the only mention fell out of the excerpt:\n%s", got)
	}
}

// A phrase the message does not carry verbatim leaves the excerpt to the
// caller's token handling. The words have to sit far enough into a long message
// that starting at the top would miss them — otherwise the whole thing fits and
// the test cannot tell the two apart.
func TestAMissingTermFallsBackToTokens(t *testing.T) {
	msg := strings.Repeat("unrelated notes about the billing tables, ", 12) +
		"the retry queue stalls on staging when the workers wake together"
	got := snippet(msg, "queue staging", nil)
	if !strings.Contains(got, "staging") {
		t.Errorf("a phrase that is not in the message lost its token fallback:\n%s", got)
	}
}

// Two clusters of the same size: the earlier one wins, so an excerpt does not
// drift down a message as it grows.
func TestEqualClustersKeepTheEarlierOne(t *testing.T) {
	cluster := "retry here, retry again, retry once more"
	msg := cluster + strings.Repeat(", filler about unrelated work", 14) + ", " + cluster
	got := snippet(msg, "retry", nil)
	if strings.HasPrefix(got, "…") {
		t.Errorf("the excerpt moved to the later cluster of the same size:\n%s", got)
	}
}
