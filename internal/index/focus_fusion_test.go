package index

import "testing"

// A question asked in plain speech carries words that are filler to a reader
// and content to the index. "How many bikes do I own" ranks on many, bikes and
// own; the session that answers it holds only the rare one, and sessions about
// how many people own things hold two. Scored on the whole query the answer
// loses to them.
//
// Scoring only the rare words instead was measured and rejected: day0bench
// gained .003 and longmemeval lost 1.5 points of hit@1 and 3 of hit@20. The
// view is not wrong, it is only right sometimes, so it is kept beside the other
// one and each session takes its better place at a price.
func TestAFocusedWinTakesABetterPlaceButPaysForIt(t *testing.T) {
	// Ranked last on the whole query, first on the rare part of it.
	ranked := make([]relevanceScored, 12)
	for i := range ranked {
		ranked[i] = relevanceScored{meta: SessionMeta{ID: string(rune('a' + i))}, score: float64(12 - i)}
	}
	ranked[11].focus = 100

	out := fuseFocus(ranked)
	at := -1
	for i, r := range out {
		if r.meta.ID == ranked[11].meta.ID {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("the session disappeared from the ranking")
	}
	// Its price puts it level with the session already holding that place, and a
	// tie goes to the view that ranked the whole query — so it lands just behind.
	if at != focusPrice+1 {
		t.Errorf("a session first on the focused view sits at %d, want %d: paid its price and no more", at, focusPrice+1)
	}
}

// The price is what stops the focused view from being the ranking. A session
// the whole query already ranks first must not be displaced by one that merely
// carries a rare word.
func TestTheFocusedViewCannotTakeTheFront(t *testing.T) {
	ranked := make([]relevanceScored, 6)
	for i := range ranked {
		ranked[i] = relevanceScored{meta: SessionMeta{ID: string(rune('a' + i))}, score: float64(6 - i)}
	}
	ranked[5].focus = 100

	out := fuseFocus(ranked)
	if out[0].meta.ID != "a" {
		t.Errorf("the front of the ranking changed hands to %q", out[0].meta.ID)
	}
}

// And a ranking where the two views agree comes back exactly as it was: this
// reorders only where they disagree.
func TestAgreementLeavesTheOrderAlone(t *testing.T) {
	ranked := make([]relevanceScored, 8)
	for i := range ranked {
		ranked[i] = relevanceScored{
			meta:  SessionMeta{ID: string(rune('a' + i))},
			score: float64(8 - i),
			focus: float64(8 - i),
		}
	}
	for i, r := range fuseFocus(ranked) {
		if r.meta.ID != ranked[i].meta.ID {
			t.Fatalf("agreeing views still reordered the ranking at %d: %q, want %q", i, r.meta.ID, ranked[i].meta.ID)
		}
	}
}
