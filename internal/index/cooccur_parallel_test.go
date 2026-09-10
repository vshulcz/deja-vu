package index

import (
	"fmt"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Tokenising every message of every session was 6 s of a build and the last
// single-threaded pass in the sidecar phase. Each session's distinct tokens are
// collected in parallel and interned afterwards in session order, so the ids,
// the document frequencies and the neighbour map are what one core produced.
func TestNeighbourMapIsTheSameOnOneCoreAndMany(t *testing.T) {
	// A word has to appear in at least three sessions and in at most a quarter
	// of them to be banded at all, so the corpus is twelve clusters of five
	// sessions, each cluster with its own pair of words.
	var ss []model.Session
	for cluster := range 12 {
		for n := range 5 {
			ss = append(ss, model.Session{
				Harness: "claude", ID: fmt.Sprintf("s%02d-%d", cluster, n), Project: "app",
				Messages: []model.Message{
					{Role: "user", Text: fmt.Sprintf("quokkabloom%02d keeps losing rows", cluster)},
					{Role: "assistant", Text: fmt.Sprintf("snorblewidget%02d rotation fixed quokkabloom%02d for good", cluster, cluster)},
					{Role: "assistant", Text: fmt.Sprintf("run %d of the same routine", n)},
				},
			})
		}
	}

	build := func(workers int) map[string][]string {
		rerankWorkers = func() int { return workers }
		dir := t.TempDir()
		buildCooccur(dir, ss)
		return readCooccur(dir)
	}
	one := build(1)
	many := build(8)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) == 0 {
		t.Fatal("no neighbours were mined, so the comparison proves nothing")
	}
	if len(one) != len(many) {
		t.Fatalf("one core mined %d tokens, eight mined %d", len(one), len(many))
	}
	for tok, want := range one {
		got, ok := many[tok]
		if !ok {
			t.Fatalf("%q has neighbours on one core and none on eight", tok)
		}
		if len(got) != len(want) {
			t.Fatalf("%q: %d neighbours on one core, %d on eight", tok, len(want), len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q neighbour %d: one core says %q, eight say %q", tok, i, want[i], got[i])
			}
		}
	}
}
