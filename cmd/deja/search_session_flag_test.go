package main

import (
	"strings"
	"testing"
)

// The flag has to reach the search options, carry its value, and behave like
// its neighbours when it is mistyped or left empty.
func TestSearchSessionFlagParses(t *testing.T) {
	o, err := parseSearch([]string{"--session", "01a00feb", "--role", "tool", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Session != "01a00feb" {
		t.Errorf("session = %q", o.Session)
	}
	if o.Role != "tool" || o.Query != "build" {
		t.Errorf("the rest of the line was lost: %#v", o)
	}
}

func TestSearchSessionFlagNeedsAValue(t *testing.T) {
	if _, err := parseSearch([]string{"--session"}); err == nil {
		t.Error("a flag with no value was accepted")
	}
}

// A near miss names the flag rather than being swallowed into the query, as it
// does for every other search flag (#755).
func TestSearchSessionFlagTypoIsNamed(t *testing.T) {
	_, err := parseSearch([]string{"--sesion", "abc", "build"})
	if err == nil || !strings.Contains(err.Error(), "--session") {
		t.Errorf("typo error = %v", err)
	}
}
