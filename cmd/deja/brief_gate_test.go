package main

import (
	"os"
	"testing"
)

// The brief is gated on "can this reader take a screen", not on "does this
// reader want colour". They were the same predicate, so NO_COLOR and TERM=dumb
// printed the usage screen to someone sitting at a terminal with an index
// (#1596). The drawing predicate keeps refusing both — TestDumbTerminalGetsNoDrawing
// pins that — because a logo and a live display are escapes, and the brief is
// text.
func TestBriefIsNotGatedOnColour(t *testing.T) {
	f := drawableCharDevice(t)

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !briefWanted(f) {
		t.Fatal("a character device with a real TERM stopped taking the brief")
	}

	t.Setenv("NO_COLOR", "1")
	if !briefWanted(f) {
		t.Error("NO_COLOR replaced the brief with the usage screen")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if !briefWanted(f) {
		t.Error("TERM=dumb replaced the brief with the usage screen")
	}
	// Drawing still refuses both: that is a separate question and #903's
	// answer to it stands.
	if defaultLogoWanted(f) {
		t.Error("TERM=dumb started drawing again")
	}
}

// The width budget asks the same question as the gate. It used to ask whether
// colour was wanted, so the readers the gate newly admits were told "do not
// cut" and their narrow lines wrapped (#1596).
func TestPrintableWidthFollowsTheScreenNotTheColour(t *testing.T) {
	f := drawableCharDevice(t)
	t.Setenv("COLUMNS", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if got := printableWidth(f); got == 0 {
		t.Error("NO_COLOR reader was told not to cut, so the brief wraps instead of fitting")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if got := printableWidth(f); got == 0 {
		t.Error("TERM=dumb reader was told not to cut")
	}
	// A pipe still reads as "do not cut": a script wants the text whole.
	r, wpipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer wpipe.Close()
	if got := printableWidth(wpipe); got != 0 {
		t.Errorf("a pipe got a width of %d, want 0", got)
	}
}
