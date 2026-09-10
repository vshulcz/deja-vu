package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The remedy that is the failing command corrected stands on its own. Before
// this it did not: the corrected line names nothing the error named — zsh says
// `no matches found` and the fix is a pair of quotes — so it was filed as a
// candidate and never served until some other session happened to run the same
// line after the same error.
func TestTheCorrectedCommandIsServedAtOnce(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "command", Text: `grep -rn "quokkabloom" --include=*.go internal`, Time: now},
			{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
			{Role: "command", Text: `grep -rn "quokkabloom" --include="*.go" internal`, Time: now.Add(2 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })

	served := FixesFor(dir, "zsh:1: no matches found: --include=*.go", 3, nil)
	if len(served) != 1 {
		t.Fatalf("the corrected command was not served: %+v", ReadFixes(dir))
	}
	if !served[0].Repaired {
		t.Errorf("served without being recognised as a repair: %+v", served[0])
	}
	if served[0].Command != `grep -rn "quokkabloom" --include="*.go" internal` {
		t.Errorf("served the wrong command: %q", served[0].Command)
	}
}

// A session that hits an error and goes on to unrelated work still yields
// nothing on its own.
func TestMovingOnStillWaitsForASecondSession(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "command", Text: `psql -c "select 1"`, Time: now},
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now.Add(time.Second)},
			{Role: "command", Text: "git status --short", Time: now.Add(2 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	if got := FixesFor(dir, "psql: connection refused on port 5432", 3, nil); len(got) != 0 {
		t.Errorf("unrelated work was served as a remedy: %+v", got)
	}
}
