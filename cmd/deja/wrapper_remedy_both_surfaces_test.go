package main

import (
	"strings"
	"testing"
)

// Two surfaces now say the same thing about a missing wrapper: the line before
// the command (#3487) and the session-start block (#3488). They share the
// predicate on purpose — the lesson #3473 recorded is that one idea spelled
// twice drifts, and the narrower copy then wins by accident. This is that
// sharing, asserted: one store, both surfaces, the same fact.
func TestBothSurfacesSayTheWrapperRemedy(t *testing.T) {
	dir := seedWrapperWall(t, func(int) string { return longWorkerCommand })

	// The wording differs because the sentences differ — the pre-tool line has
	// already named the program ("timeout is not on this machine — …"), the block
	// has not — so each is asserted as it reads, and what is shared is the fact
	// and the predicate behind it.
	before := missingProgramLine(dir, "timeout 30 "+longWorkerCommand)
	if !strings.Contains(before, "same command ran without it") {
		t.Errorf("the line before the command lost the remedy: %q", before)
	}
	block, _ := environmentBlockFrom(dir, "auto")
	if !strings.Contains(block, "the same command without `timeout`") {
		t.Errorf("the session-start block lost the remedy:\n%s", block)
	}

	// And neither surface invents one: the same store with a different command
	// after the refusal leaves both silent about a remedy.
	other := seedWrapperWall(t, func(int) string { return "launchctl print system/app.worker | grep state" })
	if line := missingProgramLine(other, "timeout 30 "+longWorkerCommand); strings.Contains(line, "same command ran without") {
		t.Errorf("the line before the command claimed a remedy no pair supports: %q", line)
	}
	if block, _ := environmentBlockFrom(other, "auto"); strings.Contains(block, "without `timeout`") {
		t.Errorf("the session-start block claimed a remedy no pair supports:\n%s", block)
	}
}
