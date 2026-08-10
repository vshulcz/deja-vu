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
	if err := checkHarness(""); err != nil {
		t.Errorf("an empty harness is the no-filter case, not an error: %v", err)
	}
	// Every registry name must pass, installed or not.
	for _, name := range sources.HarnessNames() {
		if err := checkHarness(name); err != nil {
			t.Errorf("known harness %q rejected: %v", name, err)
		}
	}
	err := checkHarness("cluade")
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
