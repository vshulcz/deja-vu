package search

import (
	"strings"
	"testing"
)

// An excerpt is offered as something someone said about the subject. Measured
// over twelve queries an agent would plausibly ask — 77 quoted lines — 6% were
// a `<teammate-message …>` envelope and 6% were deja's own credit sentence
// coming back as evidence.
func TestAnExcerptIsWhatSomeoneSaid(t *testing.T) {
	withEnvelope := "the retry budget stays at four " +
		`<teammate-message teammate_id="team-lead" summary="review">Review one commit</teammate-message>` +
		" and the cold path warms the index first"
	got := proseForSnippet(withEnvelope)
	if strings.Contains(got, "teammate-message") {
		t.Errorf("the harness's envelope is quoted as evidence:\n%s", got)
	}
	for _, want := range []string{"retry budget stays at four", "cold path warms the index"} {
		if !strings.Contains(got, want) {
			t.Errorf("the words around the envelope were lost: %q not in\n%s", want, got)
		}
	}
}

// The credit line deja asks an agent to write is written into the middle of a
// reply — "No files changed. No benchmarks run. deja-vu recalled: …" — so it is
// removed where it sits rather than by dropping the line it shares.
func TestDejaIsNotItsOwnWitness(t *testing.T) {
	text := "No files changed. No benchmarks run. deja-vu recalled: prior benchmark research. " +
		"The suite runs with -p 1 because the fixture is shared."
	got := proseForSnippet(text)
	if strings.Contains(strings.ToLower(got), "deja-vu recalled") {
		t.Errorf("deja is quoted back as evidence:\n%s", got)
	}
	if !strings.Contains(got, "-p 1 because the fixture is shared") {
		t.Errorf("the sentence after the credit was lost:\n%s", got)
	}
	if !strings.Contains(got, "No files changed") {
		t.Errorf("the sentence before the credit was lost:\n%s", got)
	}
}

// A person writing about the tag is not the tag.
func TestSomeoneNamingTheTagKeepsTheirSentence(t *testing.T) {
	text := "I removed the <teammate-message> handling from the parser, is that right?"
	if got := proseForSnippet(text); !strings.Contains(got, "removed the") {
		t.Errorf("a sentence about the tag was cut:\n%s", got)
	}
}
