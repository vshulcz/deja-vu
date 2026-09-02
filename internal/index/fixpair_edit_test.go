package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// What fixes a failing test is an edit, not a command. The pair miner only
// ever looked for the next command, so the most common failure an agent sees —
// 2,318 of 6,744 error lines on a real store — could never have a remedy
// recorded, and the next session hit it with nothing to show (#2163).
func TestATestFailureIsPairedWithTheEditThatFollowed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "edit", Text: "internal/pool/pool.go\n-\tclose(c)\n+\tc.drain()", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.4s", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want the edit: %+v", len(pairs), pairs)
	}
	if pairs[0].Edit != "internal/pool/pool.go" {
		t.Errorf("pair records %q as the edit", pairs[0].Edit)
	}
	if pairs[0].Command != "" {
		t.Errorf("an edit remedy must not be stored as a command to run: %q", pairs[0].Command)
	}
}

// A command remedy still wins where there is one: a missing binary is fixed by
// installing it, and the edit that happens to follow is not the answer.
func TestACommandRemedyStillBeatsAnEdit(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh: command not found: rg", Time: now},
		// The edit comes first and is not the answer: the session tried a
		// change, then installed the thing that was missing.
		{Role: "edit", Text: "Makefile\n-rg\n+grep", Time: now.Add(time.Minute)},
		{Role: "command", Text: "brew install ripgrep", Time: now.Add(2 * time.Minute)},
		{Role: "tool-output", Text: "installed", Time: now.Add(3 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Command != "brew install ripgrep" || pairs[0].Edit != "" {
		t.Fatalf("the command remedy was not kept: %+v", pairs)
	}
}

// An edit is one session's doing until a second session does the same after
// the same failure — the same bar a command remedy that names nothing of the
// error has to clear.
func TestAnEditRemedyIsACandidateUntilASecondSessionRepeatsIt(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
			{Role: "edit", Text: "internal/pool/pool.go\n-\tclose(c)\n+\tc.drain()", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	first := mergeFixPairs(nil, session("s1"))
	if len(first) != 1 || !first[0].Candidate {
		t.Fatalf("one sighting of an edit must be a candidate: %+v", first)
	}
	second := mergeFixPairs(first, session("s2"))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a second session doing the same edit must confirm it: %+v", second)
	}
}

// The first line of an edit record is the file it changed — unless the harness
// recorded no file, in which case it is a line of source, and a line of source
// is not a remedy.
func TestALineOfSourceIsNotAnEditRemedy(t *testing.T) {
	now := time.Now()
	pairs := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "edit", Text: "\tif err != nil {\n\t\treturn err", Time: now.Add(time.Minute)},
	}, "claude:s1", "p")
	if len(pairs) != 0 {
		t.Errorf("a line of source was stored as the file that was changed: %+v", pairs)
	}
}
