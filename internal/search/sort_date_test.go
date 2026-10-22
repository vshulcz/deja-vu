package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// On the relevance tier a few sessions hold every word and the rest hold
// some. Dated in one run, newer partial matches pushed the real ones off the
// page (#5577); they stay ahead, each part newest first.
func TestByDateKeepsStrictHitsAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	hit := func(id string, daysAgo int, strict bool) Hit {
		return Hit{Session: model.Session{ID: id, Updated: now.AddDate(0, 0, -daysAgo)}, Strict: strict}
	}
	hits := []Hit{hit("partial-new", 0, false), hit("strict-old", 9, true), hit("partial-old", 5, false), hit("strict-new", 3, true)}
	ByDate(hits)
	want := []string{"strict-new", "strict-old", "partial-new", "partial-old"}
	for i, h := range hits {
		if h.Session.ID != want[i] {
			t.Fatalf("order %d is %s, want %v", i, h.Session.ID, want)
		}
	}
}

// Under a date pool the sessions read only to check the unread estimate are
// older than the pool, with unread matches between them. On the page they
// skip those, so they count in the total and stay off it.
func TestDatePageLeavesTheSampleOff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var ss []model.Session
	for i := 0; i < 6; i++ {
		ss = append(ss, model.Session{ID: fmt.Sprintf("s%d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: "the deploy is slow", Time: now}}})
	}
	sampled := model.Session{ID: "sampled", Harness: "claude", Project: "p", Updated: now.AddDate(0, -2, 0),
		Messages: []model.Message{{Role: "user", Text: "the deploy is slow", Time: now}}}
	ss = append(ss, sampled)
	o := Options{Query: "deploy", Limit: 10, Sort: SortDate,
		Unread: &query.Unread{Sessions: 40, Length: 200, Sample: map[string]float64{"claude:sampled": 5}}}
	r, err := RunDetailed(ss, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 6 || !r.Capped || r.Total != 47 {
		t.Errorf("%d hits, capped %v, total %d; want the 6 of the pool, capped, 47", len(r.Hits), r.Capped, r.Total)
	}
	for _, h := range r.Hits {
		if h.Session.ID == "sampled" {
			t.Error("the sampled session is on a date page")
		}
	}

	// Every unread match read: the sample is an ordinary hit.
	o.Unread = &query.Unread{Sample: map[string]float64{"claude:sampled": 5}}
	if r, _ := RunDetailed(ss, o); len(r.Hits) != 7 {
		t.Errorf("with nothing left unread %d hits, want 7", len(r.Hits))
	}
}
