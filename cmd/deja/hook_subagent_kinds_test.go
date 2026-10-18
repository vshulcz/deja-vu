package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A Grok subagent spawned with the parent's context is kind "subagent_fork".
// Recall left it out of its parent's lineage, but the start digest still
// listed it as a session of its own.
func TestStartDigestLeavesOutEverySpawnedKind(t *testing.T) {
	in := []model.Session{
		{ID: "p", Harness: "grok"},
		{ID: "a", Harness: "grok", Kind: "subagent_fork", Parent: "p"},
		{ID: "b", Harness: "grok", Kind: "subagent", Parent: "p"},
		{ID: "c", Harness: "claude", Kind: "sidechain", Parent: "x"},
		{ID: "f", Harness: "grok", Kind: "fork", Parent: "p"},
	}
	got := withoutSubagentRuns(in)
	if len(got) != 2 || got[0].ID != "p" || got[1].ID != "f" {
		t.Fatalf("kept %+v, want the parent and the fork only", got)
	}
}
