package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A single sighting is worth saying and worth saying as one. Two sessions doing
// the same thing after the same error is evidence it worked; one session doing
// something is what one session did, and an agent reading it at the moment it
// is stuck has to be told which of the two it was handed.
func TestACandidateSaysNobodyConfirmedIt(t *testing.T) {
	when := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	sure := fixLine(index.FixPair{Command: "brew install coreutils", When: when})
	guess := fixLine(index.FixPair{Command: "brew install coreutils", When: when, Candidate: true})
	if sure == "" || guess == "" {
		t.Fatal("no line at all")
	}
	if sure == guess {
		t.Error("a single sighting reads exactly like a pair two sessions agree on")
	}
	if !strings.Contains(guess, "nothing confirms it worked") {
		t.Errorf("the line does not say it is unconfirmed: %q", guess)
	}
	if strings.Contains(sure, "nothing confirms") {
		t.Errorf("a confirmed pair was hedged: %q", sure)
	}
	if !strings.Contains(guess, "brew install coreutils") {
		t.Errorf("the command went missing: %q", guess)
	}
}

// And a command that failed is still no remedy, whichever half of the evidence
// it came from.
func TestACandidateThatFailedIsStillNotAnAnswer(t *testing.T) {
	if got := fixLine(index.FixPair{Command: "make build  → exit 2", Candidate: true}); got != "" {
		t.Errorf("a failed command was offered as what to do: %q", got)
	}
}
