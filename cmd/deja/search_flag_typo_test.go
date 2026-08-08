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

func TestParseSearchTrimsSurroundingSpace(t *testing.T) {
	// A leading space left the exact-match tier hunting for " token", so an
	// exact-only term (an error code, a coined name) missed on a pasted query.
	for _, in := range []string{" WHITEWORD", "WHITEWORD ", "  WHITEWORD  "} {
		o, err := parseSearch([]string{in})
		if err != nil || o.Query != "WHITEWORD" {
			t.Errorf("parseSearch(%q): query = %q err = %v", in, o.Query, err)
		}
	}
	// Space-only is empty once trimmed, so it is rejected rather than searched.
	if _, err := parseSearch([]string{"   "}); err == nil {
		t.Errorf("space-only query should be rejected")
	}
}

func TestParseSearchDashDashEndsOptions(t *testing.T) {
	// `--` stops flag parsing so a query can be the literal text of a flag.
	// Before, --json after it still turned on JSON mode and "--" was left in
	// the query, so flag-colliding text was unsearchable.
	o, err := parseSearch([]string{"--", "--json"})
	if err != nil || o.Query != "--json" || o.JSON {
		t.Fatalf("parseSearch(-- --json): query=%q json=%v err=%v", o.Query, o.JSON, err)
	}
	o, err = parseSearch([]string{"pool", "--", "--limit", "5"})
	if err != nil || o.Query != "pool --limit 5" || o.Limit != 0 {
		t.Fatalf("parseSearch(pool -- --limit 5): query=%q limit=%d err=%v", o.Query, o.Limit, err)
	}
}
