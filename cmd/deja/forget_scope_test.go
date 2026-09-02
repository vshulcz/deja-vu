package main

import (
	"strings"
	"testing"
)

// `--session` takes a prefix, so an id that names one session exactly could
// still reach every id beside it — twelve sessions for a selector that named
// one, and the count arrived afterwards, in the past tense (#870).
func TestForgetScopeRefusal(t *testing.T) {
	if err := forgetScopeRefusal("s1", 1, false); err != nil {
		t.Errorf("a single match was refused: %v", err)
	}
	err := forgetScopeRefusal("s1", 12, false)
	if err == nil {
		t.Fatal("a prefix of twelve sessions was not refused")
	}
	got := err.Error()
	for _, want := range []string{"matches 12 sessions", "--dry-run", "--all-matches"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not mention %q: %q", want, got)
		}
	}
	// Asked for explicitly, it proceeds.
	if err := forgetScopeRefusal("s1", 12, true); err != nil {
		t.Errorf("--all-matches still refused: %v", err)
	}
	// An elided id has no longer prefix to offer, so the wording differs and
	// must not send the reader after one (#859).
	err = forgetScopeRefusal("deja-2026…er-service", 2, false)
	if err == nil {
		t.Fatal("an ambiguous elided id was not refused")
	}
	if got := err.Error(); !strings.Contains(got, "the line elides") || strings.Contains(got, "--all-matches") {
		t.Errorf("elided wording = %q", got)
	}
}
