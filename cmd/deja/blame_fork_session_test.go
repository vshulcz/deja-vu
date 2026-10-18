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

// claude --resume <id> --fork-session writes a copy that names no source and
// keeps every copied record's time, so the copy starts on the same turn as its
// source and ties with it on the edit. The copy holds the source's last turn;
// the source holds none of the copy's own (#5316).
func TestBlameGivesAForkSessionCopysLineToItsSource(t *testing.T) {
	const line = "	zebracount := countRowsPastLimit(rows, 4096)"
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := search.BlameTarget{FullPath: path, Base: "main.go", Line: 2}
	commit := lineCommit{SHA: "271b28da", When: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
	at := commit.When.Add(-time.Hour)
	copied := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "the marmotquill exporter drops rows, add a zebracount", Time: at},
			{Role: sources.RoleWrote, Text: sources.WroteRecord(path, line), Time: at.Add(time.Second)},
			{Role: "assistant", Text: "added zebracount, paging fixes the exporter", Time: at.Add(2 * time.Second)},
		}
	}
	src := model.Session{Harness: "claude", ID: "aaaaaaaa", Started: at, Messages: copied()}
	fork := model.Session{Harness: "claude", ID: "ffffffff", Started: at, Messages: append(copied(),
		model.Message{Role: "user", Text: "hi", Time: at.Add(10 * time.Minute)},
		model.Message{Role: "assistant", Text: "hello", Time: at.Add(10*time.Minute + time.Second)})}

	for _, order := range [][]model.Session{{fork, src}, {src, fork}} {
		if got, ok := attributeLine(order, target, commit, nil); !ok || got.Session.ID != "aaaaaaaa" {
			t.Errorf("order %s: credited %q, want the source aaaaaaaa", forkOrder(order), got.Session.ID)
		}
	}

	// Control: the source went on after the copy too, so neither holds the
	// other's last turn and nothing says which is the copy; the tie keeps the
	// first, as before.
	went := src
	went.Messages = append(copied(), model.Message{Role: "user", Text: "source again", Time: at.Add(20 * time.Minute)})
	if got, _ := attributeLine([]model.Session{fork, went}, target, commit, nil); got.Session.ID != "ffffffff" {
		t.Errorf("siblings that both went on: credited %q, want the first", got.Session.ID)
	}
	// And a sub-agent whose parent holds its turns is not a copy of it.
	child := src
	child.ID, child.Kind, child.Parent = "agent-x1", "subagent", "ffffffff"
	if got, _ := attributeLine([]model.Session{fork, child}, target, commit, nil); got.Session.ID != "ffffffff" {
		t.Errorf("a sub-agent tie moved to %q", got.Session.ID)
	}
}
