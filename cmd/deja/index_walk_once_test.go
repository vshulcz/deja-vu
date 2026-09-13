package main

import (
	"strings"
	"testing"
)

// The freshness walk enumerates every registered store and stats every
// candidate, which on a slow volume is the longest part of `deja index`. It sat
// in an `if` initializer, and Go runs an initializer before it tests the
// condition — so a run that had already reported what it did walked the stores
// again and threw the answer away (#3500).
//
// Pinned on the source because the waste is invisible from outside: the command
// printed the same bytes either way.
func TestTheQuietOutcomeWalkRunsInsideItsBranch(t *testing.T) {
	src := string(repoFile(t, "cmd/deja/main.go"))
	if strings.Contains(src, "if fresh, n := index.UpToDate(dir, \"\"); progress.n == 0 {") {
		t.Error("the freshness walk is back in the if initializer, so it runs when its result is discarded")
	}
	if !strings.Contains(src, "if progress.n == 0 {\n\t\tfresh, n := index.UpToDate(dir, \"\")") {
		t.Error("cmdIndex no longer asks for the quiet outcome inside the branch that prints it")
	}
}
