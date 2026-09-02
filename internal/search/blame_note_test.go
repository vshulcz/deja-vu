package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// blame answers "who decided this", and a promoted note mentions the file once
// where the transcript it was made from mentions it many times — so the score
// put the transcript above its own note on every blame (#2829, the surface
// #2803 did not reach).
func TestBlamePutsANoteAboveItsSource(t *testing.T) {
	now := time.Now()
	source := model.Session{
		Harness: "claude", ID: "longs", Project: "api", Updated: now,
		Messages: []model.Message{
			{Role: "files", Text: "/work/api/pool.go", Time: now},
			{Role: "user", Text: "pool.go deadlocks under load", Time: now},
			{Role: "assistant", Text: "pool.go needs a bigger max", Time: now},
			{Role: "assistant", Text: "raised it in pool.go", Time: now},
		},
	}
	note := model.Session{
		Harness: "deja", ID: "deja-note-claude-longs", Project: "api", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "[accepted] the pool in pool.go was too small", Time: now},
		},
	}

	target := BlameTarget{FullPath: "/work/api/pool.go", Base: "pool.go", Stem: "pool"}
	hits := Blame([]model.Session{source, note}, target, BlameOptions{All: true})
	var noteAt, sourceAt = -1, -1
	for i, h := range hits {
		switch h.Session.ID {
		case "deja-note-claude-longs":
			noteAt = i
		case "longs":
			sourceAt = i
		}
	}
	if noteAt < 0 || sourceAt < 0 {
		t.Fatalf("both should be in the answer, got %d hits", len(hits))
	}
	if noteAt > sourceAt {
		t.Errorf("blame put the transcript above the note made from it")
	}
}
