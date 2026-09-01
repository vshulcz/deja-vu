package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// recall_context serves one session and said nothing about the rest, so a
// digest chosen from forty candidates read exactly like the only thing deja
// held. That is the misread #1308 fixed for the counted page — "(5 match(es))"
// reads as five exist — and blame already names what it left out.
//
// The tool returns one digest on purpose; what was missing is the number the
// agent needs to decide whether one is enough.
func TestTheContextDigestSaysHowManyItChoseFrom(t *testing.T) {
	dir := manySessionStore(t, 40)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"pipeline stalled retry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to count:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "other sessions matched") {
		t.Errorf("the digest does not say it was chosen from several:\n%s", firstLines(text, 5))
	}
	if !strings.Contains(text, "recall") {
		t.Errorf("nothing points at the tool that lists the others:\n%s", firstLines(text, 5))
	}
}

// The number itself, on a store where it is known: three sessions say
// "pipeline … stalled on retry", so the one served leaves two behind. Asserting
// only the sentence let an off-by-one through — the count is the whole point of
// the line.
func TestTheContextDigestCountsTheOthersCorrectly(t *testing.T) {
	dir := manySessionStore(t, 3)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"pipeline stalled retry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so there is nothing to count:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "2 other sessions matched") {
		t.Errorf("the count is wrong — three sessions match and one was served:\n%s", firstLines(text, 6))
	}
}

// One match is one match: a sentence about others would be a lie, and an agent
// told to go looking wastes a call.
func TestTheContextDigestIsQuietWhenItWasTheOnlyMatch(t *testing.T) {
	dir := manySessionStore(t, 1)

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"quibblesnatch"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# deja context:") {
		t.Fatalf("nothing was served, so this guards nothing:\n%s", firstLines(text, 4))
	}
	if strings.Contains(text, "other sessions matched") {
		t.Errorf("the only match was reported as one of several:\n%s", firstLines(text, 5))
	}
}
