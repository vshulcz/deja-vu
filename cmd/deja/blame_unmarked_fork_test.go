package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// opencode's --fork copies the source's messages and parts with their original
// times and names no source in the store. The edit record then ties to the
// millisecond in both sessions, and whichever came first in the hit list took
// the line: the fork, for an edit it only copied. The source started no later
// than any copy of it, so an exact tie goes to the earlier session.
func TestBlameGivesATiedLineToTheSessionACopyCameFrom(t *testing.T) {
	const (
		line     = `	println("the parent session replaced this println")`
		replaced = `	println("original line kept here for testing")`
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n"+line+"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 4}
	commit := lineCommit{SHA: "abcdef12", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	start := commit.When.Add(-time.Hour)
	editAt := start.Add(15 * time.Second)
	records := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "replace the println argument", Time: start.Add(60 * time.Millisecond)},
			{Role: sources.RoleEdit, Text: path + "\n" + replaced, Time: editAt},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: editAt},
		}
	}
	source := model.Session{Harness: "opencode", ID: "ses_source", Started: start, Messages: records()}
	fork := model.Session{Harness: "opencode", ID: "ses_fork", Title: "replace println (fork #1)",
		Started: start.Add(60 * time.Millisecond), Messages: append(records(),
			model.Message{Role: "user", Text: "now write fork.txt", Time: start.Add(time.Minute)})}

	for _, order := range [][]model.Session{{fork, source}, {source, fork}} {
		got, ok := attributeLine(order, target, commit, map[string]bool{blameSpanKey(replaced): true})
		if !ok || got.Session.ID != "ses_source" {
			t.Errorf("replaced rule, order %s,%s: credited %q, want the source", order[0].ID, order[1].ID, got.Session.ID)
		}
		got, ok = attributeLine(order, target, commit, nil)
		if !ok || got.Session.ID != "ses_source" {
			t.Errorf("written rule, order %s,%s: credited %q, want the source", order[0].ID, order[1].ID, got.Session.ID)
		}
	}

	// Control: the fork's own later edit of the same line is newer, and is the fork's.
	own := fork
	own.Messages = append(records(), model.Message{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: start.Add(2 * time.Minute)})
	if got, _ := attributeLine([]model.Session{source, own}, target, commit, nil); got.Session.ID != "ses_fork" {
		t.Errorf("a fork that wrote the line again later is credited %q, want the fork", got.Session.ID)
	}
}
