package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestFixPairsKeepTheCommandThatSettledTheError(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "200 OK", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	if pairs[0].Command != "curl --max-time 5 example.internal" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
	}
}

// The command that did not settle it is not a fix: the same error right after
// it means the session was still failing.
func TestFixPairsDropACommandTheErrorSurvived(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1"); len(pairs) != 0 {
		t.Errorf("a command the error outlived was stored as a fix: %+v", pairs)
	}
}

// Sequence alone is 13% precise on a real store — the next command is usually
// the session moving on. A pair survives the build only with a second reason:
// the command names what the error named, or the same remedy recurs.
func TestBuildFixesDropsTheUnrelatedNextCommand(t *testing.T) {
	now := time.Now()
	unrelated := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "git status --short", Time: now.Add(time.Minute)},
		},
	}
	related := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "brew services start postgresql && psql -c 'select 1'", Time: now.Add(time.Minute)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{unrelated, related}, func(s model.Session) string { return s.Harness + ":" + s.ID })
	got := ReadFixes(dir)
	if len(got) != 1 {
		t.Fatalf("want only the related pair, got %d: %+v", len(got), got)
	}
	if got[0].Key != "claude:s2" {
		t.Errorf("kept the wrong pair: %+v", got[0])
	}
	// And a lookup finds it from the error text alone, wherever in a paste the
	// line sits.
	found := FixesFor(dir, "traceback follows\npsql: connection refused on port 5432\n", 3)
	if len(found) != 1 {
		t.Errorf("the pair is not findable from the pasted error: %+v", found)
	}
}
