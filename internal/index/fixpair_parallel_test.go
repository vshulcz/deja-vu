package index

import (
	"fmt"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Mining runs per session on all cores now — it was 9.4 s of a 51 s build, the
// longest of the four sidecars. What lands on file must not depend on which
// worker finished first, so the same corpus is mined on one core and on eight
// and the tables are compared row by row.
func TestMinedPairsAreTheSameOnOneCoreAndMany(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var ss []model.Session
	for i := range 40 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i), Project: "app", Updated: now,
			Messages: []model.Message{
				{Role: "command", Text: fmt.Sprintf("grep -rn \"quokkabloom%d\" --include=*.go internal", i), Time: now},
				{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
				{Role: "command", Text: fmt.Sprintf("grep -rn \"quokkabloom%d\" --include=\"*.go\" internal", i), Time: now.Add(2 * time.Second)},
				{Role: "tool-output", Text: "3 matches", Time: now.Add(3 * time.Second)},
				{Role: "command", Text: "make check", Time: now.Add(4 * time.Second)},
				{Role: "tool-output", Text: fmt.Sprintf("--- FAIL: TestSnorbleWidget%d", i), Time: now.Add(5 * time.Second)},
				{Role: "edit", Text: fmt.Sprintf("internal/widget/snorble%d.go\n-\tone\n+\ttwo", i), Time: now.Add(6 * time.Second)},
			},
		})
	}
	keyOf := func(s model.Session) string { return s.Harness + ":" + s.ID }

	build := func(workers int) []FixPair {
		rerankWorkers = func() int { return workers }
		dir := t.TempDir()
		buildFixes(dir, ss, keyOf)
		return ReadFixes(dir)
	}
	one := build(1)
	many := build(8)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) == 0 {
		t.Fatal("nothing was mined, so the comparison proves nothing")
	}
	if len(one) != len(many) {
		t.Fatalf("one core mined %d pairs, eight mined %d", len(one), len(many))
	}
	for i := range one {
		if one[i] != many[i] {
			t.Fatalf("row %d differs:\n one core: %+v\n eight:    %+v", i, one[i], many[i])
		}
	}
}
