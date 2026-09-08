package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The cap lives in RunDetailed, which only the exact path goes through, so the
// two tiers that build their own hits printed the whole retrieval window: 50
// sessions for `--limit 3`, and 50 again for a query with no flag, where the
// default is 15 (#3345).
func TestCapHitsBoundsWhatEachTierHandsBack(t *testing.T) {
	hits := make([]Hit, 50)
	for i := range hits {
		hits[i] = Hit{Session: model.Session{ID: string(rune('a' + i%26))}}
	}
	cases := []struct {
		name    string
		limit   int
		all     bool
		want    int
		wantCap bool
	}{
		{"a limit is honoured", 3, false, 3, true},
		{"no flag takes the default", 0, false, 15, true},
		{"--all takes everything", 0, true, 50, false},
		{"a limit past the end caps nothing", 100, false, 50, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, capped := CapHits(hits, c.limit, c.all)
			if len(got) != c.want || capped != c.wantCap {
				t.Errorf("CapHits(50 hits, limit %d, all %v) = %d hits capped %v, want %d capped %v",
					c.limit, c.all, len(got), capped, c.want, c.wantCap)
			}
		})
	}
}
