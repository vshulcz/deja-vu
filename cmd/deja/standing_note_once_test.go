package main

import (
	"strings"
	"testing"
)

// A decision that holds across a project is one fact, however many files it is
// said in front of. The line was deduped whole and its head names the file, so
// on a real store one accepted note reached 154 different files — a line on
// every edit of the session, all of it the same sentence.
func TestAStandingDecisionIsOneFact(t *testing.T) {
	first := "hook_tool.go has been worked on in 6 sessions, last 2026-09-10" +
		standingLabel + "the repository description leads with the task"
	second := "install.go has been worked on in 9 sessions, last 2026-09-10" +
		standingLabel + "the repository description leads with the task"
	if dedupeFact(first) != dedupeFact(second) {
		t.Errorf("the same standing decision counts twice:\n  %q\n  %q",
			dedupeFact(first), dedupeFact(second))
	}
	if !strings.Contains(dedupeFact(first), "repository description") {
		t.Errorf("the fact lost what it says: %q", dedupeFact(first))
	}
}

// A decision about a file is that file's, so two of them are two facts.
func TestTwoFileDecisionsAreTwoFacts(t *testing.T) {
	first := "render.go has been worked on in 6 sessions — prior decision: the renderer never re-wraps"
	second := "retry.go has been worked on in 6 sessions — prior decision: the retry budget stays at four"
	if dedupeFact(first) == dedupeFact(second) {
		t.Error("two different file decisions were counted as one fact")
	}
}
