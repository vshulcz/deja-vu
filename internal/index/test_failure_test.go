package index

import "testing"

// A failing test is the most common thing an agent hits in a Go repo — 2,318
// of the 6,744 error lines on the store this was measured against — and it was
// rejected by name, listed with the shapes that identify nothing. The test name
// identifies plenty: it is stable across runs and across machines, which is
// more than most error strings manage.
func TestANamedTestFailureIsFriction(t *testing.T) {
	line, ok := FrictionLine("--- FAIL: TestGrokSinceHandlesEveryStampShape (0.09s)")
	if !ok {
		t.Fatal("a failing test is not read as friction")
	}
	// The duration is this run's, not the failure's. Left in, every run is its
	// own piece of friction and none of them ever reaches a second session.
	if line != "--- FAIL: TestGrokSinceHandlesEveryStampShape" {
		t.Errorf("the run's duration stayed in the line: %q", line)
	}
	other, _ := FrictionLine("    --- FAIL: TestGrokSinceHandlesEveryStampShape (12.40s)")
	if other != line {
		t.Errorf("two runs of the same failing test do not compare equal: %q vs %q", other, line)
	}
}

// The summary lines name nothing, which is why they were rejected in the first
// place, and they stay rejected.
func TestTheBareFailSummariesAreStillNotFriction(t *testing.T) {
	for _, l := range []string{
		"FAIL",
		"FAIL\tgithub.com/vshulcz/deja-vu/internal/index\t20.4s",
		"--- FAIL",
		`t.Fatalf("--- FAIL: %s", name)`,
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line that identifies nothing was read as friction: %q", l)
		}
	}
}

// The classifier decides whether a command could have been the remedy, and
// several git subcommands are a read or a write depending on the argument.
func TestGitSubcommandsThatDependOnTheirArgument(t *testing.T) {
	for _, cmd := range []string{
		"git stash list | head -3",
		"git worktree list",
		"git tag -l",
		"git remote -v",
	} {
		if !InspectionCommand(cmd) {
			t.Errorf("a read was classified as a change: %q", cmd)
		}
	}
	// Going to look at CI is the miner's question, not the hook's: it is where
	// an agent goes after a failure, not what it did about it.
	if !investigationCommand("until gh pr checks 2059; do sleep 20; done") {
		t.Error("a poll loop around a read was classified as a remedy")
	}
	for _, cmd := range []string{
		"git stash push -u -m wip",
		"git worktree add /tmp/wt main",
		"git tag v1.2.3",
		"git remote add origin git@github.com:x/y",
	} {
		if InspectionCommand(cmd) {
			t.Errorf("a change was classified as a read: %q", cmd)
		}
	}
}
