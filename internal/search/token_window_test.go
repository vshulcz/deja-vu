package search

import (
	"strings"
	"testing"
)

// Proximity is the signal that the query's words belong to one thought.
// Measuring between the FIRST place each word appears is close to the opposite
// of measuring that: a message that says "connection" in its opening line and
// then discusses "connection pool exhausted" as a phrase four paragraphs down
// was scored on the four paragraphs.
func TestTheWindowIsTheTightestOneNotTheFirstOne(t *testing.T) {
	text := strings.ToLower(
		"connection issues have been on my mind all week. " +
			strings.Repeat("unrelated discussion of scheduling and travel plans. ", 20) +
			"the connection pool exhausted under load")
	got := tokenWindow(text, []string{"connection", "pool", "exhausted"})
	// "connection pool exhausted" is 25 characters; anything near that is the
	// phrase, anything near a thousand is the whole message.
	if got > 40 {
		t.Errorf("window is %d characters: measured across the message rather than across the phrase", got)
	}
}

// A token that is genuinely absent still means no window at all, and one token
// has no window to speak of.
func TestAMissingTokenHasNoWindow(t *testing.T) {
	if got := tokenWindow("the connection pool exhausted", []string{"connection", "kafka"}); got != 0 {
		t.Errorf("a query token absent from the text produced a window of %d", got)
	}
	if got := tokenWindow("the connection pool exhausted", []string{"connection"}); got != 0 {
		t.Errorf("a single token produced a window of %d", got)
	}
}

// The tightest cluster can be anywhere, including behind a long run of one of
// the words — the case the occurrence cap has to survive.
func TestTheCapDoesNotHideTheTightestCluster(t *testing.T) {
	text := strings.ToLower(
		strings.Repeat("pool ", windowOccurrenceCap*2) + "connection pool exhausted")
	got := tokenWindow(text, []string{"connection", "pool", "exhausted"})
	if got > 40 {
		t.Errorf("window is %d characters: the cap stopped before the cluster", got)
	}
}
