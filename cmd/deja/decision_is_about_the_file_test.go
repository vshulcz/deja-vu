package main

import "testing"

// A decision earns the word by being about the file. Read from a real store: of
// the 63 lines that called something the prior decision about a file, none
// mentioned the file or the package it sits in — one incident diagnosis was
// offered as the prior decision about five different main.go, and an answer to a
// question about Zed's wiring as the decision about three different SKILL.md.
func TestTheDecisionLabelIsEarned(t *testing.T) {
	about := []struct{ path, text string }{
		{"/work/app/config.go", "config.go must load the ARENAGUARD sentinel before any read"},
		{"/work/app/render.go", "the renderer never re-wraps a paragraph that already fits"},
		{"/work/app/internal/retry/backoff.go", "retry backoff stays at four attempts"},
		{"/work/app/notes.jsonl", "a note keeps its own state, not the session's"},
	}
	for _, c := range about {
		if got := decisionLabelFor(c.path, c.text); got != decisionLabel {
			t.Errorf("a decision about the file was not called one:\n  %s\n  %s", c.path, c.text)
		}
	}

	elsewhere := []struct{ path, text string }{
		{"/work/app/cmd/dfbot/main.go", "Причина из логов: explore start rejected by the moderation check"},
		{"/work/app/.claude/skills/release/SKILL.md", "You asked whether the Zed wiring was already fixed; it was, in #1571"},
		{"/work/app/cmd/deja/doctor.go", "The largest content-quality problem is not retrieval order"},
	}
	for _, c := range elsewhere {
		if got := decisionLabelFor(c.path, c.text); got != endedLabel {
			t.Errorf("a conclusion about something else was called this file's decision:\n  %s\n  %s", c.path, c.text)
		}
	}
}

// And the same sentence in front of five files is one fact, whichever label it
// arrived under.
func TestOneSentenceIsOneFactWhateverTheLabel(t *testing.T) {
	a := "main.go has been worked on in 6 sessions" + endedLabel + "explore start rejected by the moderation check"
	b := "main.go has been worked on in 9 sessions" + endedLabel + "explore start rejected by the moderation check"
	if dedupeFact(a) != dedupeFact(b) {
		t.Error("the same closing sentence counts twice")
	}
	c := "render.go has been worked on in 6 sessions" + decisionLabel + "the renderer never re-wraps"
	if dedupeFact(a) == dedupeFact(c) {
		t.Error("two different facts were counted as one")
	}
}
