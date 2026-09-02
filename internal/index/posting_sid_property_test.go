package index

import (
	"math"
	"math/rand"
	"testing"
)

// The session id is written as a delta now, so the property that matters is
// that any block of postings survives the round trip — not a list of cases
// someone thought of. The extremes are seeded deliberately because a zigzag
// over a uint32 subtraction is where an off-by-one lives: 0, 1<<31, MaxUint32,
// and the steps between them (#492).
func TestAnyBlockOfPostingsSurvivesTheRoundTrip(t *testing.T) {
	extremes := []uint32{0, 1, 2, math.MaxUint32, math.MaxUint32 - 1, 1 << 31, (1 << 31) - 1, 1<<31 + 1}
	rng := rand.New(rand.NewSource(11))
	for round := 0; round < 300; round++ {
		n := 1 + rng.Intn(12)
		posts := make([]posting, 0, n)
		off := int64(rng.Intn(1000))
		for i := 0; i < n; i++ {
			// Offsets must ascend: a block is what writeBucket produced, and
			// it sorts by offset. The session ids are deliberately not sorted.
			off += int64(1 + rng.Intn(5000))
			var sid uint32
			switch rng.Intn(3) {
			case 0:
				sid = extremes[rng.Intn(len(extremes))]
			case 1:
				sid = uint32(rng.Intn(50))
			default:
				sid = rng.Uint32()
			}
			posts = append(posts, posting{Off: off, Sid: sid, Tool: rng.Intn(2) == 1})
		}
		got := decodePostings(encodePostings(posts))
		want := sortedUniquePostings(posts)
		if len(got) != len(want) {
			t.Fatalf("round %d: %d postings came back from %d", round, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round %d posting %d: got %+v, want %+v (block %+v)", round, i, got[i], want[i], want)
			}
		}
	}
}

// And the delta is scoped to its own block: two blocks encoded one after the
// other must not read each other's last session id.
func TestOneBlocksSessionIdsDoNotLeakIntoTheNext(t *testing.T) {
	first := []posting{{Off: 1, Sid: math.MaxUint32}, {Off: 2, Sid: 5}}
	second := []posting{{Off: 1, Sid: 7}}
	if got := decodePostings(encodePostings(second)); len(got) != 1 || got[0].Sid != 7 {
		t.Fatalf("a block read on its own gave %+v", got)
	}
	// Encoding the first block before the second must not change what the
	// second one decodes to.
	_ = encodePostings(first)
	if got := decodePostings(encodePostings(second)); len(got) != 1 || got[0].Sid != 7 {
		t.Fatalf("the previous block's session id leaked: %+v", got)
	}
}
