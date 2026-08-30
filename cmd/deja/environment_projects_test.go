package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func metas(projects ...string) []index.SessionMeta {
	var out []index.SessionMeta
	for _, p := range projects {
		out = append(out, index.SessionMeta{Project: p})
	}
	return out
}

// The block says "this machine", and an error that only ever appeared while
// working on one repository is that repository's business. Counting sessions
// alone, a string this project's own tests print reads as a fact about the
// machine and is then told to an agent working somewhere else.
func TestAWallOfOneRepositoryIsNotAFactAboutTheMachine(t *testing.T) {
	// One tree under the names it arrives with: a worktree, a temporary
	// checkout, the bare directory. Two of those are not two projects, which is
	// why the bar is three.
	if spansProjects(metas("run", "run", "compare/fixes", "run"), 9) {
		t.Error("a wall from one tree under two names passed as machine-wide")
	}
	if !spansProjects(metas("api", "web", "infra"), 9) {
		t.Error("a wall hit in three separate projects was dropped")
	}
}

// A session with no project name says nothing either way and must not be
// counted as one.
func TestAnUnnamedProjectDoesNotCount(t *testing.T) {
	if spansProjects(metas("api", "", "", "web"), 9) {
		t.Error("blank project names were counted toward the bar")
	}
}

// A machine with one or two projects cannot spread a wall across three, and
// everything it hits is all it does. Asking for spread there silenced the block
// for exactly the people with the least history to fall back on.
func TestASmallMachineIsNotAskedToSpread(t *testing.T) {
	if !spansProjects(metas("only", "only", "only"), 1) {
		t.Error("a one-project machine was told its own wall is not its own")
	}
	if spansProjects(metas("run", "compare/fixes"), 9) {
		t.Error("a wall of one tree passed on a machine that has plenty of projects")
	}
}
