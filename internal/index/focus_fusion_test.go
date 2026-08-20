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
	// Under reciprocal rank fusion the two views trade places instead of one
	// paying the other a fixed toll: first on the focused view is worth a lot,
	// last on the whole query little, and the sum decides. What matters is not
	// the exact place — that is the fusion constant's business — but that the
	// session climbs out of the tail without taking the front.
	if at == 0 {
		t.Error("a session convincing on the rare word alone took the front")
	}
	if at >= len(out)/2 {
		t.Errorf("a session first on the focused view sits at %d of %d: the "+
			"focused view bought it nothing", at, len(out))
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

// The fusion constant damps how much one top place is worth. Summing 1/rank
// rather than 1/(k+rank) lets a single first place outweigh being second on
// both views, which is the behaviour reciprocal rank fusion exists to avoid
// (Cormack, Clarke & Buettcher, SIGIR 2009). Measured on a real store, dropping
// the constant to zero puts the ranking back where it stood before the fusion.
func TestFusionDampsASingleTopPlace(t *testing.T) {
	ranked := make([]relevanceScored, 10)
	// "a" leads the whole query and is last on the rare part of it, and "i"
	// leads the rare part while sitting ninth on the whole query. "b" is second
	// on both. Second twice is the better bet, and only a damped sum says so:
	// undamped, one first place carries either of the other two past it.
	focus := []float64{1, 90, 80, 70, 60, 50, 40, 30, 100, 10}
	for i := range ranked {
		ranked[i] = relevanceScored{
			meta:  SessionMeta{ID: string(rune('a' + i))},
			score: float64(10 - i),
			focus: focus[i],
		}
	}
	if out := fuseFocus(ranked); out[0].meta.ID != "b" {
		t.Errorf("front went to %q, want b: second on both views beats first on one",
			out[0].meta.ID)
	}
}
