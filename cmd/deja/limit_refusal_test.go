package main

import (
	"strings"
	"testing"
)

// `files` and `how` take --limit, so a reader arrives at `blame` and `last`
// having just been told the flag works. Both refuse it, both for a reason, and
// neither said what to type instead — the sentence `friction` and `install`
// have printed all along (#3405).
func TestTheRefusalNamesWhatTheCommandTakes(t *testing.T) {
	if _, _, _, err := parseLast([]string{"--limit", "3"}); err == nil {
		t.Fatal("last took --limit")
	} else if !strings.Contains(err.Error(), "deja last 3") {
		t.Errorf("last says %q, without the count it takes", err)
	}
	if _, _, _, err := parseBlame([]string{"x.go", "--limit", "3"}); err == nil {
		t.Fatal("blame took --limit")
	} else if !strings.Contains(err.Error(), "--all") {
		t.Errorf("blame says %q, without the flag that widens it", err)
	}
	// Every other flag keeps the plain refusal: the note is about the one
	// spelling a reader carries over from the commands beside these.
	if _, _, _, err := parseLast([]string{"--nonesuch"}); err == nil ||
		strings.Contains(err.Error(), "deja last 3") {
		t.Errorf("an unrelated flag gets the count note: %v", err)
	}
}
