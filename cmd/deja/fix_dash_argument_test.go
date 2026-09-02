package main

import (
	"bytes"
	"strings"
	"testing"
)

// Go's own test output is the commonest thing a reader pastes at `deja fix`,
// and every failing line of it starts with a dash. Without the escape the
// command deja prints for the reader cannot be run (#2799).
func TestFixTakesALineThatStartsWithADash(t *testing.T) {
	dir := t.TempDir()
	line := "--- FAIL: TestX (0.01s)"

	var out bytes.Buffer
	if err := runFix(dir, []string{line}, &out); err != nil {
		t.Errorf("a pasted failure line was read as a flag: %v", err)
	}
	out.Reset()
	if err := runFix(dir, []string{"--", line}, &out); err != nil {
		t.Errorf("the escape did not take the line either: %v", err)
	}
	// The flags still have to be flags after it: an unknown one is a mistake
	// worth reporting, not a search term.
	if err := runFix(dir, []string{"--json"}, &out); err == nil {
		t.Error("--json was swallowed as text")
	}
}

// The hint is a command the reader pastes, so it has to carry the escape when
// the line needs it.
func TestTheFixHintEscapesADashLeadingLine(t *testing.T) {
	line := "--- FAIL: TestX (0.01s)"
	got := fixHintCommand(line)
	if !strings.HasPrefix(got, "deja fix -- ") {
		t.Errorf("the hint is not runnable as printed: %q", got)
	}
	if plain := fixHintCommand("pq: canceling statement due to user request"); strings.Contains(plain, " -- ") {
		t.Errorf("a line that needs no escape got one: %q", plain)
	}
}
