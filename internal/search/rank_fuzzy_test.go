package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestFuzzyHitsScoreByFrequency(t *testing.T) {
	ts := time.Now().Add(-time.Hour) // same recency for both -> isolates BM25
	dense := model.Session{ID: "dense", Updated: ts, Messages: []model.Message{
		{Role: "user", Text: "frobnicator frobnicator frobnicator crashed, frobnicator again"},
	}}
	sparse := model.Session{ID: "sparse", Updated: ts, Messages: []model.Message{
		{Role: "user", Text: "frobnicator crashed once"},
	}}
	o := Options{Query: "frobnicatr", All: true, FuzzyVariants: map[string][]string{"frobnicatr": {"frobnicator"}}}
	hits, err := Run([]model.Session{sparse, dense}, o)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits=%d err=%v", len(hits), err)
	}
	for _, h := range hits {
		if h.Score <= 0 {
			t.Fatalf("fuzzy hit %s scored zero (variant not counted)", h.Session.ID)
		}
	}
	if hits[0].Session.ID != "dense" {
		t.Fatalf("equal recency but frequency ignored: %s(%.3f) before %s(%.3f)",
			hits[0].Session.ID, hits[0].Score, hits[1].Session.ID, hits[1].Score)
	}
}
