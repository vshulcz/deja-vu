package index

import "testing"

// Asking deja about an error must not teach deja that the error happened
// again. `deja fix` prints the error with the date beside it and the command
// underneath; both land in the next session's tool output, where the header
// read as a fresh sighting and the command below it became a remedy for the
// error it was quoting. Found on a real store as the pair
// `command not found: python · 2026-05-18`.
func TestDejasOwnFixReportIsNotFriction(t *testing.T) {
	for _, l := range []string{
		"command not found: python · 2026-05-18",
		"zsh:1: command not found: timeout · 2026-01-02",
		"  ran next: brew install coreutils",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("deja's own report read as friction: %q", l)
		}
	}
	// The same errors, as a tool actually prints them, still are.
	for _, l := range []string{
		"zsh:1: command not found: timeout",
		"ModuleNotFoundError: No module named 'yaml'",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a real error stopped being friction: %q", l)
		}
	}
	// A date at the end is deja's separator, not a general rule: a line that
	// happens to end in a date without it keeps its meaning.
	if _, ok := FrictionLine("fatal: could not read repository state at 2026-05-18"); !ok {
		t.Error("a line ending in a date lost its meaning")
	}
}
