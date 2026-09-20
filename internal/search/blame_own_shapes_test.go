package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Every new shape deja prints that names a file has to arrive with its
// recogniser, or blame reads its own answer back as the file's history. Three
// shapes came after the first pass: the silence sentence was rewritten when the
// written side landed and the recogniser kept pinning the old wording, the line
// answer can now be asked for as JSON, and `--git-note` puts the same
// attribution where `git log --notes=deja` prints it into a transcript (#3723,
// #3773, #1330).
func TestBlameDoesNotQuoteTheShapesThatCameAfterTheFirstPass(t *testing.T) {
	now := time.Now().UTC()
	target := BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool"}
	for _, tc := range []struct {
		name string
		own  string
		mark string
	}{
		{
			// The answer for a line nothing is attributed to says "deja"
			// nowhere, and the cheap check that decides whether to filter a
			// message at all was looking for that word — so this shape, which
			// names the file in its first line, was never filtered.
			name: "the answer that never says deja",
			own:  "pool.go:3 last changed in 33ec9160 · 2026-09-18 · fix: one pool per shard\n  no indexed session wrote this line or the lines this commit replaced — nothing to say about this line",
			mark: "last changed in",
		},
		{
			name: "the line answer as JSON",
			own:  `{"kind":"deja.blame-line","schema_version":2,"file":"pool.go","line":3,"commit":{"sha":"33ec9160"},"attributed":true,"rule":"wrote","matched":"var conns = newPool(shardCount)","session":{"harness":"claude","id":"2915986c","project":"app","ctx":"deja ctx 2915986c"}}`,
			mark: "deja.blame-line",
		},
		{
			// The quoted turn under a line answer: the label is deja's, the
			// sentence after it is a transcript's, and a session that ran
			// blame keeps both.
			name: "the turn the line answer quotes",
			own:  "pool.go:3 written in claude · 2915986c · app\n  said just before this edit: internal/pool.go opened a connection per shard and the shard count moves at runtime",
			mark: "said just before this edit",
		},
		{
			name: "the note git log prints",
			own:  "deja: pool.go:3 written in claude · 2915986c · app (rule: replaced)\nwhy, in full: deja ctx 2915986c",
			mark: "written in claude",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model.Session{
				Harness: "claude", ID: "s1", Project: "app", Updated: now,
				Messages: []model.Message{
					{Role: sources.RoleToolOutput, Text: tc.own, Time: now},
					{Role: "assistant", Text: "internal/pool.go keeps one connection per shard because the driver pools per process", Time: now.Add(time.Second)},
				},
			}
			hits := Blame([]model.Session{s}, target, BlameOptions{All: true})
			if len(hits) != 1 {
				t.Fatalf("want the one session, got %d", len(hits))
			}
			joined := strings.Join(hits[0].Snippets, " | ")
			if strings.Contains(joined, tc.mark) {
				t.Errorf("blame quoted its own answer back: %q", joined)
			}
			if !strings.Contains(joined, "one connection per shard") {
				t.Errorf("the sentence that says why is missing: %q", joined)
			}
		})
	}
}
