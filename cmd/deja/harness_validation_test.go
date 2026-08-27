package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A typo'd --harness used to be indistinguishable from a real harness with no
// sessions — both said "matched nothing under harness X" — so `--harness
// cluade` read as "you have no claude history" (#1113).
func TestCheckHarnessRejectsOnlyUnknownNames(t *testing.T) {
	empty := ""
	if err := checkHarness(&empty); err != nil {
		t.Errorf("an empty harness is the no-filter case, not an error: %v", err)
	}
	// Every registry name must pass, installed or not.
	for _, name := range sources.HarnessNames() {
		if err := checkHarness(&name); err != nil {
			t.Errorf("known harness %q rejected: %v", name, err)
		}
	}
	// The name the index run prints for the notes store, which the check now
	// rewrites to the one it is stored under (#2191).
	printed := "notes"
	if err := checkHarness(&printed); err != nil || printed != "deja" {
		t.Errorf("notes was not accepted as the printed name for deja: %v, left as %q", err, printed)
	}
	typo := "cluade"
	err := checkHarness(&typo)
	if err == nil {
		t.Fatal("a misspelled harness was accepted")
	}
	// The refusal has to hand back the real names, or it is a dead end.
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("refusal does not list the known harnesses: %v", err)
	}
}

// blame accepts --harness like search does, so it must validate it the same way
// — the check used to live only on search, last, stats and the MCP blame, so
// `deja blame file --harness cluade` read as "nobody touched this file" (#1113).
func TestBlameCLIRejectsUnknownHarness(t *testing.T) {
	err := runBlame(t.TempDir(), []string{"parser.go", "--harness", "cluade"})
	if err == nil || !strings.Contains(err.Error(), "not a harness") {
		t.Fatalf("blame did not name the typo'd harness: %v", err)
	}
}

// The validators exist, but the bug is a command that parses the flag and
// forgets to call them (blame did). Pin the wiring on every surface that takes
// these flags so a refactor cannot quietly drop the call again.
func TestFilterValidationWiredAcrossCommands(t *testing.T) {
	dir := t.TempDir()
	rejects := func(err error) bool {
		return err != nil && (strings.Contains(err.Error(), "not a harness") || strings.Contains(err.Error(), "not a role"))
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"search --harness", func() error { return runSearch(dir, []string{"q", "--harness", "cluade"}, "") }},
		{"search --role", func() error { return runSearch(dir, []string{"q", "--role", "toool"}, "") }},
		{"last --harness", func() error { return cmdLast(dir, []string{"--harness", "cluade"}, "") }},
		{"last --role", func() error { return cmdLast(dir, []string{"--role", "toool"}, "") }},
		{"stats --harness", func() error { return runStats(dir, []string{"--harness", "cluade"}) }},
		{"stats --role", func() error { return runStats(dir, []string{"--role", "toool"}) }},
	}
	for _, c := range cases {
		if err := c.run(); !rejects(err) {
			t.Errorf("%s: expected a typo rejection, got %v", c.name, err)
		}
	}
}
