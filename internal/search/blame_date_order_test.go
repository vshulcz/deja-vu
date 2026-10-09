package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Among sessions that named the file as a path, the listing reads newest
// first. By score, one that repeated the path more sat above a newer one, and
// the dates ran 06-29, 06-27, 06-28.
func TestBlameListsPathSessionsNewestFirst(t *testing.T) {
	target := BlameTarget{FullPath: "/repo/internal/pool/pool.go", Base: "pool.go", Stem: "pool"}
	day := func(d int) time.Time { return time.Date(2026, 6, d, 10, 0, 0, 0, time.UTC) }
	say := func(n int) []model.Message {
		var ms []model.Message
		for range n {
			ms = append(ms, model.Message{Role: "assistant", Text: "changed internal/pool/pool.go to retire idle connections sooner"})
		}
		return ms
	}
	ss := []model.Session{
		{ID: "older-busier", Harness: "claude", Project: "/repo", Updated: day(27), Messages: say(4)},
		{ID: "newest", Harness: "claude", Project: "/repo", Updated: day(29), Messages: say(1)},
		{ID: "middle", Harness: "claude", Project: "/repo", Updated: day(28), Messages: say(1)},
	}
	hits := Blame(ss, target, BlameOptions{All: true})
	var got []string
	for _, h := range hits {
		got = append(got, h.Session.ID)
	}
	want := []string{"newest", "middle", "older-busier"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}
