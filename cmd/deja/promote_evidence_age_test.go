package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note is a distillation, so it is as old as the session it came from. When
// promote stamped the note with the moment it ran, promoting a January session
// minted the freshest thing in the store: the note ranked first and `deja ctx`
// handed the agent the stale value while the August session that corrected it
// sat below, unread.
func TestPromotedNoteRanksByEvidenceAgeNotFilingTime(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "old.jsonl"), "oldsess1", []string{
		`{"type":"user","sessionId":"oldsess1","timestamp":"2026-01-10T10:00:00Z","message":{"role":"user","content":"what is the payment gateway retry timeout"}}`,
		`{"type":"assistant","sessionId":"oldsess1","timestamp":"2026-01-10T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"the payment gateway retry timeout is 30 seconds"}]}}`,
	})
	fresh := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(root, "-tmp-app", "new.jsonl"), "freshsess", []string{
		`{"type":"user","sessionId":"freshsess","timestamp":"` + fresh + `","message":{"role":"user","content":"we changed the payment gateway retry timeout, what is it now"}}`,
		`{"type":"assistant","sessionId":"freshsess","timestamp":"` + fresh + `","message":{"role":"assistant","content":[{"type":"text","text":"the payment gateway retry timeout is now 5 seconds"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"oldsess1", "--note", "payment gateway retry timeout is 30 seconds"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	hits := searchHits(t, dir, "payment gateway retry timeout")
	if len(hits) < 3 {
		t.Fatalf("want note + both transcripts, got %d hits", len(hits))
	}
	if got := hits[0].Session.ID; got != "freshsess" {
		t.Fatalf("the newer session must outrank a note distilled from a January one, got %s:%s at rank 1",
			hits[0].Session.Harness, got)
	}
	// And the note still sits in front of the transcript it came from: the
	// evidence date must not cost it that place.
	note, src := -1, -1
	for i, h := range hits {
		switch h.Session.ID {
		case "deja-note-claude-oldsess1":
			note = i
		case "oldsess1":
			src = i
		}
	}
	if note < 0 || src < 0 || note > src {
		t.Fatalf("note must rank above its own source transcript: note=%d source=%d", note, src)
	}
}
