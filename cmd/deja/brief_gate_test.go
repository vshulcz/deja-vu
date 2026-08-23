package main

import (
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
