package main

import "testing"

// The cross-paired arm is only worth reading if no question is ever asked in
// the project that holds its answer. Pairing by position makes that easy to get
// wrong the day two neighbouring chains share a project.
func TestCrossPairingNeverAsksAtHome(t *testing.T) {
	crossed := []PromptChainRef{
		{"a", "one", "a"},
		{"b", "one", "b"},
		{"c", "two", "c"},
		{"d", "three", "d"},
	}
	for i, c := range crossed {
		away := crossed[(i+1)%len(crossed)]
		if away.Project == c.Project {
			away = crossed[(i+2)%len(crossed)]
		}
		if away.Project == c.Project {
			t.Errorf("%q was asked in its own project %q", c.ID, c.Project)
		}
	}
}
