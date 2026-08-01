package main

import (
	"strings"
	"testing"
)

// Checking the flag ahead of the argument cost two runs to learn two missing
// things, in the order deja checks them rather than the order the command
// reads (#820).
func TestShowNamesTheMissingIdBeforeTheMissingFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"nothing at all", []string{}, "show needs id-prefix"},
		{"json without an id", []string{"--json"}, "show needs id-prefix"},
		{"json with an id", []string{"--json", "s1"}, "requires --harness"},
		{"harness but no id", []string{"--json", "--harness", "claude"}, "show needs id-prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseShow(tc.args)
			if err == nil {
				t.Fatalf("parseShow(%v) returned no error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseShow(%v) = %v, want %q", tc.args, err, tc.want)
			}
		})
	}

	// A complete invocation still parses.
	o, err := parseShow([]string{"--json", "--harness", "claude", "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if o.id != "s1" || o.harness != "claude" || !o.json {
		t.Errorf("parsed = %+v", o)
	}
}
