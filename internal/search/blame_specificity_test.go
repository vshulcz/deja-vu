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
	// Asked by a bare filename deja has no tree to prefer, so the rule sits
	// out and nothing moves: crediting whatever directories a session happened
	// to write handed `blame Makefile` to three other projects, measured on a
	// real store. What follows is the query that carries a path.
	bare := order(BlameTarget{FullPath: "/work/api/ingest.go", Base: "ingest.go", Stem: "ingest"})
	if len(bare) < 4 {
		t.Fatalf("every session names the file: %v", bare)
	}
	place := func(list []string, id string) int {
		for i, got := range list {
			if got == id {
				return i
			}
		}
		return -1
	}

	// And the same when the caller names the path: the session that spells it
	// out in full is not pushed down by a newer one that says less.
	full := order(BlameTarget{FullPath: "/work/api/internal/index/ingest.go", Base: "ingest.go", Stem: "ingest"})
	// The two that wrote the path asked about come first, however new or
	// talkative the others are: the bare mention says the name four times and
	// is the newest, and the file in another tree wrote a path of its own.
	for _, ahead := range []string{"abs", "rel"} {
		for _, behind := range []string{"base", "other"} {
			if place(full, ahead) > place(full, behind) {
				t.Errorf("%s came after %s: %v", ahead, behind, full)
			}
		}
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
