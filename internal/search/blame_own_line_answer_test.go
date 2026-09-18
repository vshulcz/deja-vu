package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A transcript that ran deja keeps its output, and every line of the line-level
// answer names the file it was asked about — so blame ranked its own past
// answer as the history of the file and quoted it back. #1330 closed that for
// the shapes that existed then; the line answer is a new one (#1181).
func TestBlameDoesNotQuoteItsOwnLineAnswer(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	own := strings.Join([]string{
		"pool.go:3 last changed in 33ec9160 · 2026-09-18 · fix: one pool per shard",
		"  written in claude · 2915986c-9f7 · app",
		"  replaced: var conns = newPool(shardCount * connsPerShard)",
		"  why, in full: deja ctx 2915986c-9f7",
	}, "\n")
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: own, Time: now},
			{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver pools per process", Time: now.Add(time.Second)},
		},
	}
	hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
	if len(hits) != 1 {
		t.Fatalf("want the one session, got %d", len(hits))
	}
	joined := strings.Join(hits[0].Snippets, " | ")
	if strings.Contains(joined, "last changed in") || strings.Contains(joined, "why, in full") {
		t.Errorf("blame quoted its own answer back: %q", joined)
	}
	if !strings.Contains(joined, "one connection per shard") {
		t.Errorf("the sentence that says why is missing: %q", joined)
	}
}
