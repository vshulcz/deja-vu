package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// blame tells every agent "most specific mentions come first". Asked by a bare
// filename the order was recency, and a session naming a different file with
// the same basename came ahead of the ones naming the path asked about (#2840).
func TestBlameOrdersBySpecificityBeforeRecency(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	session := func(id, text string, at time.Time) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "api", Updated: at,
			Messages: []model.Message{{Role: "user", Text: text, Time: at}},
		}
	}
	// Oldest first, so recency alone would reverse them.
	ss := []model.Session{
		session("abs", "we edited /work/api/internal/index/ingest.go and fixed the watermark", day(10)),
		session("rel", "the change went into internal/index/ingest.go, one function", day(11)),
		session("base", "ingest.go needed a second pass, nothing else", day(12)),
		session("other", "we touched cmd/tools/ingest.go in the other repo entirely", day(13)),
	}

	order := func(target BlameTarget) []string {
		hits := Blame(ss, target, BlameOptions{All: true})
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Session.ID)
		}
		return out
	}
	bare := order(BlameTarget{Base: "ingest.go", Stem: "ingest"})
	if len(bare) < 4 {
		t.Fatalf("every session names the file: %v", bare)
	}
	// The two that name the path asked about come before the bare mention and
	// before the file in another tree.
	place := func(list []string, id string) int {
		for i, got := range list {
			if got == id {
				return i
			}
		}
		return -1
	}
	// Every session that wrote a path around the name says more about which
	// file it means than the one that wrote the name alone, whichever tree it
	// names — with only a base name to go on, deja cannot prefer a tree, and
	// the promise it makes is about specificity, not about guessing.
	for _, ahead := range []string{"abs", "rel", "other"} {
		if place(bare, ahead) > place(bare, "base") {
			t.Errorf("bare name: %s came after the bare mention: %v", ahead, bare)
		}
	}
	// And the deepest path wins over a shallower one.
	if place(bare, "abs") > place(bare, "rel") || place(bare, "abs") > place(bare, "other") {
		t.Errorf("bare name: the fullest path did not come first: %v", bare)
	}
	// And the same when the caller names the path: the session that spells it
	// out in full is not pushed down by a newer one that says less.
	full := order(BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"})
	if place(full, "other") < place(full, "rel") || place(full, "other") < place(full, "abs") {
		t.Errorf("a file in another tree outranked the path asked about: %v", full)
	}
}
