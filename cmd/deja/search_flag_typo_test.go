package main

import (
	"strings"
	"testing"
)

// Folding a misspelled flag into the query turned a working search into "you
// have no such memory" (#755).
func TestNearestSearchFlag(t *testing.T) {
	typos := map[string]string{
		"--limti":   "--limit",
		"--projekt": "--project",
		"--harnes":  "--harness",
		"--jsonn":   "--json",
		"--rol":     "--role",
	}
	for typo, want := range typos {
		if got := nearestSearchFlag(typo); got != want {
			t.Errorf("nearestSearchFlag(%q) = %q, want %q", typo, got, want)
		}
	}
	// A query may legitimately contain a dash, and a real flag is not a typo.
	for _, ok := range []string{
		"--retry", "--json", "--limit", "--re", "--all",
		"pool", "-x", "--", "--zzzzzzzz", "--nosuchflag",
	} {
		if got := nearestSearchFlag(ok); got != "" {
			t.Errorf("nearestSearchFlag(%q) = %q, want silence", ok, got)
		}
	}
}

func TestParseSearchRejectsAMisspelledFlag(t *testing.T) {
	if _, err := parseSearch([]string{"pool", "--limti", "5"}); err == nil ||
		!strings.Contains(err.Error(), "did you mean --limit") {
		t.Errorf("err = %v", err)
	}
	// The query survives when nothing looks like a typo.
	o, err := parseSearch([]string{"--retry", "budget"})
	if err != nil || o.Query != "--retry budget" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
	o, err = parseSearch([]string{"pool", "--limit", "5"})
	if err != nil || o.Limit != 5 || o.Query != "pool" {
		t.Errorf("limit = %d query = %q err = %v", o.Limit, o.Query, err)
	}
}
