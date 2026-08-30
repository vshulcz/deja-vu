package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func said(id string, lines ...string) model.Session {
	s := model.Session{ID: id, Harness: "claude", Project: "p"}
	for i, l := range lines {
		role := "assistant"
		if i == 0 {
			role = "user"
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: l})
	}
	return s
}

// Two sessions that settled the same thing say it in one sentence and differ in
// everything around it. Hashing every line that held any word of the question
// made the small talk part of what the session was saying, so the two came back
// as different answers and the block spent both its slots on one.
func TestTwoSessionsSayingOneThingFillOneSlot(t *testing.T) {
	terms := []string{"queue", "throwing", "away"}
	first := said("a",
		"the watermark is what keeps the queue from throwing work away",
		"noted, moving on to the invoice")
	second := said("b",
		"the watermark is what keeps the queue from throwing work away",
		"right, and the queue for that runs nightly")
	if !sameAnswerAs([]model.Session{first}, second, terms) {
		t.Error("two sessions saying the same sentence took a slot each")
	}
}

// The same rule must not merge two answers to one question. A session that
// asked what everyone else asked is not a duplicate of them: what it settled is
// its own, and here the two settled opposite numbers.
func TestTwoAnswersToOneQuestionKeepTheirSlots(t *testing.T) {
	terms := []string{"payment", "webhook", "retries"}
	old := said("old",
		"how many retries for the payment webhook",
		"we set the payment webhook retries to 10")
	recent := said("new",
		"how many retries for the payment webhook",
		"we cut the payment webhook retries to 3")
	if sameAnswerAs([]model.Session{old}, recent, terms) {
		t.Error("two different answers to the same question were merged into one")
	}
}
