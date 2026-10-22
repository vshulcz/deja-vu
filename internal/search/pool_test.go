package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func poolCands(n int, now time.Time) []PoolCandidate {
	cands := make([]PoolCandidate, n)
	for i := range cands {
		cands[i] = PoolCandidate{
			Session: model.Session{
				Harness: "claude", ID: fmt.Sprintf("s%04d", i), Title: "tidy the logs",
				Updated: now.Add(-time.Duration(30+i) * 24 * time.Hour), Words: 400 + 37*i, Held: 10 + i%7,
			},
			Records: 1 + i%5,
		}
	}
	return cands
}

// The pool is what the manifest and the postings rank highest, the sample is
// spread over the rest, and the rest is counted at its estimated length
// (#5467).
func TestPoolKeepsTheBestAndSamplesTheRest(t *testing.T) {
	now := time.Now()
	cands := poolCands(600, now)
	// The one that should lead: fresh, the word in its title, said often.
	cands[417].Session.Updated = now
	cands[417].Session.Title = "the deploy pipeline"
	cands[417].Records = 9
	// A note and a session agents keep recalling, both old.
	cands[300].Session.Harness = notesHarness
	cands[301].Session.ID = "worn"
	// A manifest row from before Words was kept.
	cands[302].Session.Words, cands[302].Session.Held = 0, 0
	o := Options{Query: "deploy", RecallWorn: map[string]int{"worn": 9}}

	if PoolSize(5) != poolMin || PoolSize(100) != 100*poolPerHit {
		t.Errorf("pool for 5 hits %d, for 100 hits %d", PoolSize(5), PoolSize(100))
	}
	if keep, u := Pool(cands, o, len(cands), 0.1); keep != nil || u.Sessions != 0 {
		t.Fatalf("every candidate fits, yet the pool left %d out", u.Sessions)
	}
	keep, u := Pool(cands, o, 50, 0.1)
	if !keep[417] {
		t.Error("the fresh session with the word in its title is not in the pool")
	}
	read := 0
	for _, k := range keep {
		if k {
			read++
		}
	}
	if want := 50 + poolSample; read != want || len(u.Sample) != poolSample {
		t.Errorf("read %d with a sample of %d, want %d and %d", read, len(u.Sample), want, poolSample)
	}
	if read+u.Sessions != len(cands) {
		t.Errorf("read %d and left %d of %d", read, u.Sessions, len(cands))
	}
	if u.Length <= 0 {
		t.Errorf("the unread sessions have no length: %v", u.Length)
	}
	for key, est := range u.Sample {
		if est <= 0 {
			t.Errorf("sample %s carries no estimate", key)
		}
	}

	// A word in every record still has an occurrence rate.
	if keep, _ := Pool(cands, o, 50, 1); !keep[417] {
		t.Error("with the word in every record the pool lost the session that leads")
	}
}

// The sessions retrieval left unread count toward the total and toward BM25's
// corpus, with or without a sample read to check their length.
func TestUnreadSessionsCountInTheCorpus(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{Harness: "claude", ID: "a", Updated: now, Words: 40, Messages: []model.Message{
			{Role: "user", Text: "the deploy is stuck"}, {Role: "assistant", Text: "rolled the deploy back and the queue drained"}}},
		{Harness: "claude", ID: "b", Updated: now.Add(-time.Hour), Words: 900, Messages: []model.Message{
			{Role: "assistant", Text: "deploy " + strings.Repeat("unrelated words about the queue ", 30)}}},
		{Harness: "claude", ID: "c", Updated: now.Add(-2 * time.Hour), Words: 60, Messages: []model.Message{
			{Role: "user", Text: "why does the deploy keep failing"}}},
	}
	plain, err := RunDetailed(ss, Options{Query: "deploy", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Total != 3 {
		t.Fatalf("total %d, want 3", plain.Total)
	}
	for _, u := range []*query.Unread{
		{Sessions: 500, Length: 500 * 120, Sample: map[string]float64{"claude:c": 20}},
		{Sessions: 500, Length: 500 * 120},
	} {
		r, err := RunDetailed(ss, Options{Query: "deploy", Limit: 2, Unread: u})
		if err != nil {
			t.Fatal(err)
		}
		if r.Total != 503 || !r.Capped || len(r.Hits) != 2 {
			t.Errorf("sample %v: total %d capped %v hits %d, want 503 capped 2", u.Sample, r.Total, r.Capped, len(r.Hits))
		}
		if r.Hits[0].Score == plain.Hits[0].Score {
			t.Errorf("sample %v: 500 unread sessions left the scores as they were", u.Sample)
		}
	}
}
