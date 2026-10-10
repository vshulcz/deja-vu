package main

import (
	"strings"
	"testing"
)

// A --word search does not have, after the query, was folded into it, so
// `deja goroutines --bogus` searched for both words and found nothing, and
// `deja goroutines --help` searched for "--help".
func TestSearchRefusesAFlagItDoesNotHave(t *testing.T) {
	for _, args := range [][]string{
		{"goroutines", "--bogus"},
		{"goroutines", "--max-hits", "3"},
	} {
		_, err := parseSearch(args)
		if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "after `--`") {
			t.Errorf("parseSearch(%q) = %v, want an unknown-flag refusal", args, err)
		}
	}
	// Still queries: the text after --, a query that starts with a dash, a
	// phrase with a dash inside, and a single dash.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pool", "--", "--bogus"}, "pool --bogus"},
		{[]string{"--retry", "budget"}, "--retry budget"},
		{[]string{"pool", "a --dash inside"}, "pool a --dash inside"},
		{[]string{"-v"}, "-v"},
		{[]string{"rollback", "-1"}, "rollback -1"},
	} {
		o, err := parseSearch(tc.args)
		if err != nil || o.Query != tc.want {
			t.Errorf("parseSearch(%q) = %q, %v; want the query %q", tc.args, o.Query, err, tc.want)
		}
	}
}

func TestBareSearchHelpPrintsSearchHelp(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "goroutines", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja search [flags] <query>") {
		t.Errorf("want the search help:\n%s", out)
	}
}
