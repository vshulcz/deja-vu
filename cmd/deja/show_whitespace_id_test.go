package main

import (
	"strings"
	"testing"
)

// A whitespace-only session id is no id at all: `deja show "   "` sailed past
// the missing-id refusal because the check only looked for an empty string,
// and a leading empty argument was silently ignored instead of refused (#5936).
func TestShowWhitespaceOnlyIDIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"whitespace only", []string{"   "}},
		{"empty", []string{""}},
		{"empty before an id", []string{"", "sess1234"}},
		{"whitespace before an id", []string{"   ", "sess1234"}},
		{"whitespace after an id", []string{"sess1234", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseShow(tc.args)
			if err == nil {
				t.Fatalf("parseShow(%v) returned no error", tc.args)
			}
			if !strings.Contains(err.Error(), "show needs an id prefix") {
				t.Errorf("parseShow(%v) = %v, want the missing-id refusal", tc.args, err)
			}
		})
	}

	// A real id still parses.
	o, err := parseShow([]string{"sess1234"})
	if err != nil {
		t.Fatal(err)
	}
	if o.id != "sess1234" {
		t.Errorf("parsed id = %q, want %q", o.id, "sess1234")
	}
}
