package index

import (
	"math/rand"
	"reflect"
	"testing"
)

// The swap rung reads each word's postings once and intersects sorted lists
// instead of rebuilding a map per word per neighbour (#5540). It has to hand
// the scan exactly what the maps did: the same records, in record order, with
// the first word's posting for each, the last read when a record repeats.
func TestSwapIntersectionMatchesTheMapIntersection(t *testing.T) {
	rng := rand.New(rand.NewSource(5562))
	randomPostings := func() []posting {
		n := rng.Intn(40)
		out := make([]posting, n)
		for i := range out {
			out[i] = posting{Off: int64(rng.Intn(60)), Sid: uint32(rng.Intn(1000)), Tool: rng.Intn(2) == 0}
		}
		return out
	}
	for round := 0; round < 2000; round++ {
		terms := make([][]posting, 1+rng.Intn(4))
		for j := range terms {
			terms[j] = randomPostings()
		}
		neighbour := randomPostings()
		swap := rng.Intn(len(terms))

		var sets []map[int64]posting
		for j, posts := range terms {
			if j == swap {
				posts = neighbour
			}
			set := map[int64]posting{}
			for _, p := range posts {
				set[p.Off] = p
			}
			sets = append(sets, set)
		}
		want := intersectPostingMaps(sets)

		byTerm := make([][]posting, len(terms))
		for j, posts := range terms {
			byTerm[j] = uniqueByOffset(posts)
		}
		swapped := uniqueByOffset(neighbour)
		kept, from := keptIntersection(byTerm, swap)
		got := withSwapped(kept, from, swap, swapped)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d, swap %d of %d:\n got %v\nwant %v", round, swap, len(terms), got, want)
		}
	}
}
