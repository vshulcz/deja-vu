package main

import (
	"strings"
	"testing"
)

// Like an unknown --harness (#1113), a typo'd --role was indistinguishable from
// a real role with no matches — `--role toool` said "matched nothing under role
// toool" instead of naming the mistake.
func TestCheckRoleRejectsOnlyUnknownRoles(t *testing.T) {
	if err := checkRole(""); err != nil {
		t.Errorf("an empty role is the no-filter case, not an error: %v", err)
	}
	for _, r := range knownRoles {
		if err := checkRole(r); err != nil {
			t.Errorf("known role %q rejected: %v", r, err)
		}
	}
	err := checkRole("toool")
	if err == nil {
		t.Fatal("a misspelled role was accepted")
	}
	if !strings.Contains(err.Error(), "user") || !strings.Contains(err.Error(), "assistant") {
		t.Errorf("refusal does not list the known roles: %v", err)
	}
}
