package main

import (
	"strings"
	"testing"
)

// A flag deja takes on another command is not a typo and is not near a search
// flag by edit distance, so it fell through into the query and a search that
// would have found everything reported nothing (#2249).
func TestParseSearchRejectsAFlagFromAnotherCommand(t *testing.T) {
	for flag, command := range map[string]string{
		"--offset":  "show",
		"--deep":    "doctor",
		"--dry-run": "forget",
		"--span":    "restore",
		"--tag":     "remember",
	} {
		_, err := parseSearch([]string{"retry", flag, "2"})
		if err == nil {
			t.Errorf("%s was taken as part of the query", flag)
			continue
		}
		if !strings.Contains(err.Error(), flag) || !strings.Contains(err.Error(), command) {
			t.Errorf("%s: %v — the message should name the flag and the command that takes it", flag, err)
		}
	}

	// The escape hatch stays open: after `--` the same text is a query.
	o, err := parseSearch([]string{"--", "retry", "--offset"})
	if err != nil || o.Query != "retry --offset" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
	// And a dash that is nobody's flag is still a query word.
	o, err = parseSearch([]string{"--retry", "budget"})
	if err != nil || o.Query != "--retry budget" {
		t.Errorf("query = %q err = %v", o.Query, err)
	}
}
