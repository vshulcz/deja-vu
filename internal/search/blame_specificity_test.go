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
		// The bare mention is the newest and says the name most often, so only
		// the rule about naming a path keeps it behind the others.
		session("base", "ingest.go ingest.go ingest.go needed a second pass, nothing else about ingest.go", day(13)),
		session("other", "we touched cmd/tools/ingest.go in the other repo entirely", day(12)),
	}

	order := func(target BlameTarget) []string {
		hits := Blame(ss, target, BlameOptions{All: true})
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Session.ID)
		}
		return out
	}
	// The shape ResolveBlamePath builds: it always makes the path absolute, so
	// a target with an empty FullPath is one production cannot produce and a
	// test written against it proves nothing.
	bare := order(BlameTarget{FullPath: "/work/api/ingest.go", Base: "ingest.go", Stem: "ingest"})
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
	// Among the sessions that wrote a path, the answer is still what it was —
	// how much each one has to say about the file. Ordering on how deep the
	// path is instead put one mention of an unrelated file above a session
	// that had worked on this one all week.
	// And the same when the caller names the path: the session that spells it
	// out in full is not pushed down by a newer one that says less.
	full := order(BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"})
	if place(full, "other") < place(full, "rel") || place(full, "other") < place(full, "abs") {
		t.Errorf("a file in another tree outranked the path asked about: %v", full)
	}
}

// Measured on a real store: counting the directories a session wrote made a
// deeply nested file of the same name in another project the most specific
// mention there is, and every session that had actually worked on the file
// fell off the first page (#2840).
func TestBlameDoesNotPreferADeepUnrelatedPath(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-72 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-72 * time.Hour),
			Text: "internal/index/ingest.go again: ingest.go holds the watermark, ingest.go stamps it, and ingest.go is where the pass ends",
		}},
	}
	elsewhere := model.Session{
		Harness: "opencode", ID: "elsewhere", Project: "other", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: "/var/folders/jn/T/opencode/d3fvxl/apps/image2video/cmd/dfprocessing/ingest.go failed to build",
		}},
	}
	hits := Blame([]model.Session{worked, elsewhere},
		BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"},
		BlameOptions{All: true})
	if len(hits) != 2 {
		t.Fatalf("both sessions name the file: %d hits", len(hits))
	}
	if hits[0].Session.ID != "worked" {
		t.Errorf("a deeper path in another project outranked the session that worked on the file: %s first", hits[0].Session.ID)
	}
}
