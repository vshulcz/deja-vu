package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A line deja stored is a line deja can look up. The shell-position rule reads
// `zsh:1: no matches found: …` and stores the line without the marker, and the
// stored line is not friction on its own — so `deja fix` was handed a line out
// of its own table, answered "nothing recorded for that line", and offered as
// the closest thing it held the line it had just been given. 106 of the 1,310
// error lines on a real store are that shape.
func TestAStoredErrorLineIsFoundByItsOwnText(t *testing.T) {
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

	stored := ReadFixes(dir)
	if len(stored) != 1 {
		t.Fatalf("want one pair, got %+v", stored)
	}
	if got := FixesFor(dir, stored[0].Error, 3, nil); len(got) != 1 {
		t.Fatalf("the stored line %q found nothing: %+v", stored[0].Error, got)
	}
	// The marker the shell printed still works, and so does the whole line with
	// other output around it.
	if got := FixesFor(dir, "make check\nzsh:1: no matches found: --include=*.go\nexit 1", 3, nil); len(got) != 1 {
		t.Errorf("the pasted output stopped matching: %+v", got)
	}
	// The fallback is deja recognising its own wording, not a search: a line
	// that only contains the stored one is still a miss, which is what the
	// "closest it holds" hint is for.
	if got := FixesFor(dir, "no matches found", 3, nil); len(got) != 0 {
		t.Errorf("a partial line matched: %+v", got)
	}
}
