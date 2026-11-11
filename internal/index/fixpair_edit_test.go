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
	first := judgeFixes(session("s1"))
	if len(first) != 1 || !first[0].Candidate {
		t.Fatalf("one sighting of an edit must be a candidate: %+v", first)
	}
	second := judgeFixes(append(first, session("s2")...))
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

func TestEditRemedyMinesSymbolAndCausalWhy(t *testing.T) {
	now := time.Now()
	s1 := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "assistant", Text: "**Root cause:** `Pool.drain` closed the work channel because workers were still reading in-flight items.", Time: now.Add(10 * time.Second)},
		{Role: "edit", Text: "internal/pool/pool.go\nfunc (p *Pool) drain() {\n-\tclose(p.work)\n+\tp.wg.Wait()\n+\tclose(p.work)", Time: now.Add(20 * time.Second)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.2s", Time: now.Add(30 * time.Second)},
	}, "claude:s1", "app")
	if len(s1) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(s1), s1)
	}
	if s1[0].Symbol != "Pool.drain" {
		t.Errorf("Symbol = %q, want %q", s1[0].Symbol, "Pool.drain")
	}
	if s1[0].Why != "`Pool.drain` closed the work channel because workers were still reading in-flight items." {
		t.Errorf("Why = %q", s1[0].Why)
	}

	// Second session confirms the edit with only stdlib calls in the hunk and no
	// explanation; judgeFixes must keep the first sighting's Symbol and Why and
	// must not mistake strings.TrimSpace for a domain symbol.
	s2 := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now.Add(time.Hour)},
		{Role: "edit", Text: "internal/pool/pool.go\n\tname := strings.TrimSpace(raw)\n-\tclose(p.work)\n+\tp.wg.Wait()", Time: now.Add(time.Hour + time.Minute)},
	}, "claude:s2", "app")
	if len(s2) != 1 {
		t.Fatalf("s2 got %d pairs", len(s2))
	}
	if s2[0].Symbol != "" {
		t.Errorf("stdlib call should not be mined as Symbol, got %q", s2[0].Symbol)
	}
	judged := judgeFixes(append(s1, s2...))
	if len(judged) != 1 || judged[0].Candidate {
		t.Fatalf("expected 1 confirmed pair, got %+v", judged)
	}
	if judged[0].Symbol != "Pool.drain" || judged[0].Why == "" {
		t.Errorf("judgeFixes lost causal metadata from earlier sighting: %+v", judged[0])
	}
}
