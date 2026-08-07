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
