package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja promote` tells the reader the note now outranks the raw transcript.
// The ranking arranges that — and then the semantic rerank, which knows
// nothing about the two being a distillation and its source, put the
// transcript back on top. Measured on a real store: four queries, the note
// below its own session in every one, and in one of them absent from fifty
// results (#2083).
func TestAReorderPutsANoteBackAboveItsSource(t *testing.T) {
	note := search.Hit{Session: model.Session{Harness: "deja", ID: "deja-note-claude-longs", Project: "proj"}}
	source := search.Hit{Session: model.Session{Harness: "claude", ID: "longs", Project: "proj"}}
	other := search.Hit{Session: model.Session{Harness: "claude", ID: "elsewhere", Project: "proj"}}

	// What a rerank can hand back: the source above the note it came from.
	hits := []search.Hit{source, other, note}
	search.LiftNotesAboveTheirSource(hits)
	if hits[0].Session.Harness != "deja" {
		t.Errorf("the transcript is still above its own note: %v", hitIDs(hits))
	}

	// A note with no source in the answer, and a source with no note, are both
	// left where the ranking put them.
	hits = []search.Hit{other, note}
	search.LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != "elsewhere" {
		t.Errorf("a note with no source here was lifted over an unrelated session: %v", hitIDs(hits))
	}
	hits = []search.Hit{other, source}
	search.LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != "elsewhere" {
		t.Errorf("a source with no note here was moved: %v", hitIDs(hits))
	}
}

func hitIDs(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.Harness+":"+h.Session.ID)
	}
	return out
}

// Anything that re-sorts a ranked set has to restore the order the ranking
// chose. There are two such places and both are in this file, so the guard is
// on the source: an end-to-end version would need a sidecar built to rank the
// transcript above its own note, which is the very thing being prevented.
func TestEveryReorderRestoresTheNoteOrder(t *testing.T) {
	src, err := os.ReadFile("embed.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, fn := range []string{"func maybeRerank(", "func maybeSemantic("} {
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("%s is gone; this guard needs updating", fn)
		}
		j := strings.Index(body[i:], "\n}\n")
		if j < 0 {
			t.Fatalf("%s has no end; this guard needs updating", fn)
		}
		if !strings.Contains(body[i:i+j], "search.LiftNotesAboveTheirSource(") {
			t.Errorf("%s re-sorts the hits and does not restore the note order", fn)
		}
	}
}
