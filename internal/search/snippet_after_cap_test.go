package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// quotableCorpus is a pile of sessions that all match, so the difference
// between "quote everything that matched" and "quote what is served" is
// visible.
func quotableCorpus(n int) []model.Session {
	now := time.Now()
	ss := make([]model.Session, 0, n)
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("session %d: the billing exporter drops every third retry, and the backoff counted from zero", i)
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: fmt.Sprintf("proj%d", i),
			Updated:  now.AddDate(0, 0, -i),
			Messages: []model.Message{{Role: "user", Text: text, Time: now.AddDate(0, 0, -i)}},
		})
	}
	return ss
}

// Quoting every session that matched made a search cost the same at --limit 1
// and at --limit 100 — 806 ms either way on a 31 MB store, a third of the
// bytes it allocated (#3544). What a reader sees must not change with it.
func TestTheQuotesAreTheSameWhateverTheCap(t *testing.T) {
	ss := quotableCorpus(120)
	all, err := Run(ss, Options{Query: "billing exporter retry", All: true})
	if err != nil {
		t.Fatal(err)
	}
	few, err := Run(ss, Options{Query: "billing exporter retry", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(few) != 3 {
		t.Fatalf("capped search returned %d hits, want 3", len(few))
	}
	for i, h := range few {
		if len(h.Snippets) == 0 {
			t.Fatalf("served hit %d (%s) came back with nothing quoted", i, h.Session.ID)
		}
		if h.Session.ID != all[i].Session.ID {
			t.Fatalf("hit %d is %s capped and %s uncapped", i, h.Session.ID, all[i].Session.ID)
		}
		if len(h.Snippets) != len(all[i].Snippets) {
			t.Fatalf("hit %d quotes %d passages capped and %d uncapped", i, len(h.Snippets), len(all[i].Snippets))
		}
		for j := range h.Snippets {
			if h.Snippets[j] != all[i].Snippets[j] {
				t.Errorf("hit %d quote %d differs:\n capped:   %q\n uncapped: %q", i, j, h.Snippets[j], all[i].Snippets[j])
			}
		}
	}
	// And every hit an uncapped search returns is quoted too — the deferral
	// must not leave the tail of a `--all` answer blank.
	for _, h := range all {
		if len(h.Snippets) == 0 {
			t.Fatalf("uncapped hit %s came back with nothing quoted", h.Session.ID)
		}
	}
}

// The work itself: below the window markEarlierAttempts compares, a hit that
// the cap will drop carries its chosen passages and no rendered quote.
func TestABigSearchDoesNotQuoteWhatItWillNotServe(t *testing.T) {
	ss := quotableCorpus(120)
	hits, err := runScored(ss, Options{Query: "billing exporter retry"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < earlierAttemptWindow+10 {
		t.Fatalf("corpus produced %d hits, too few to show the difference", len(hits))
	}
	for i := 0; i < earlierAttemptWindow; i++ {
		if len(hits[i].Snippets) == 0 {
			t.Fatalf("hit %d is inside the comparison window and was not quoted", i)
		}
	}
	quoted := 0
	for i := earlierAttemptWindow; i < len(hits); i++ {
		if len(hits[i].Snippets) > 0 {
			quoted++
		}
		if len(hits[i].snipTexts) == 0 {
			t.Fatalf("hit %d kept no passage to quote later", i)
		}
	}
	if quoted != 0 {
		t.Errorf("%d hits below the window were quoted before anything asked for them", quoted)
	}
}
