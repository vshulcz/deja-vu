package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func blameOrder(hits []BlameHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

// A session holding a write or edit of the file ranks above one that only
// named it, and a write a fork copied stays its source's: recency alone put
// the parent that asked for the file above the sub-agent that wrote it, and the
// fork above the source (#5317).
func TestBlameListsTheWriterAboveASessionThatNamedTheFile(t *testing.T) {
	const path = "/home/u/work/app/notes.txt"
	target := BlameTarget{FullPath: path, Base: "notes.txt", Stem: "notes"}
	at := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	parent := model.Session{Harness: "claude", ID: "bbbbbbbb", Project: "/home/u/work/app", Started: at, Updated: at.Add(30 * time.Minute), Messages: []model.Message{
		{Role: "user", Text: "have a subagent write the release notes into " + path, Time: at},
		{Role: "assistant", Text: "done", Time: at.Add(30 * time.Minute)},
	}}
	child := model.Session{Harness: "claude", ID: "x1", Kind: "subagent", Parent: "bbbbbbbb", Project: "/home/u/work/app", Started: at.Add(time.Second), Updated: at.Add(3 * time.Second), Messages: []model.Message{
		{Role: "user", Text: "Write the release notes into " + path, Time: at.Add(time.Second)},
		{Role: sources.RoleWrote, Text: sources.WroteRecord(path, "release notes for the marmotquill exporter"), Time: at.Add(3 * time.Second)},
		{Role: sources.RoleFiles, Text: path, Time: at.Add(3 * time.Second)},
	}}
	if got := blameOrder(Blame([]model.Session{parent, child}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "x1" {
		t.Errorf("sub-agent case: order %v, want x1 first", got)
	}

	// The fork: a copy holding the source's edit at its original time, plus
	// turns of its own, so it is newer.
	copied := func() []model.Message {
		return []model.Message{
			{Role: "user", Text: "add the exporter notes", Time: at},
			{Role: sources.RoleEdit, Text: path + "\nold notes line here", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "updated " + path, Time: at.Add(2 * time.Second)},
		}
	}
	src := model.Session{Harness: "claude", ID: "aaaaaaaa", Project: "/home/u/work/app", Started: at, Updated: at.Add(2 * time.Second), Messages: copied()}
	for _, kind := range []string{"fork", ""} {
		fork := model.Session{Harness: "claude", ID: "ffffffff", Project: "/home/u/work/app", Started: at, Updated: at.Add(time.Hour), Messages: append(copied(),
			model.Message{Role: "user", Text: "hi", Time: at.Add(time.Hour)})}
		if kind != "" {
			fork.Kind, fork.Parent = kind, "aaaaaaaa"
		}
		if got := blameOrder(Blame([]model.Session{fork, src}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "aaaaaaaa" {
			t.Errorf("fork marked=%v: order %v, want the source first", kind != "", got)
		}
	}

	// Control: two sessions that both wrote the file keep newest first.
	other := child
	other.ID, other.Kind, other.Parent, other.Updated = "x2", "", "", at.Add(time.Hour)
	other.Messages = []model.Message{{Role: "user", Text: "Write the release notes again into " + path, Time: at.Add(time.Hour)}, {Role: sources.RoleWrote, Text: sources.WroteRecord(path, "a different notes line written later"), Time: at.Add(time.Hour)}}
	if got := blameOrder(Blame([]model.Session{child, other}, target, BlameOptions{All: true})); len(got) != 2 || got[0] != "x2" {
		t.Errorf("two writers: order %v, want the newer x2 first", got)
	}
}
