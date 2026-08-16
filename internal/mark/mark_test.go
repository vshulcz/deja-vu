package mark

import "testing"

// The mark was hand-drawn into four places before this package existed: the SVG
// assets, the stats card, the stats page and the local viewer. Those literals
// are being replaced by Paths, and this pins the unit-scale output to what they
// held — so the change is a move, not a redraw.
func TestPathsMatchTheHandDrawnLiterals(t *testing.T) {
	want := []Path{
		{Fill: "#8787af", D: "M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v1h-18ZM2 6h18v1h-18ZM2 7h3v1h-3ZM7 7h8v1h-8ZM17 7h3v1h-3ZM2 8h3v1h-3ZM7 8h8v1h-8ZM17 8h3v1h-3ZM2 9h3v1h-3ZM7 9h8v1h-8ZM17 9h3v1h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v1h-2ZM5 15h12v1h-12ZM19 15h2v1h-2ZM5 16h12v1h-12ZM19 16h2v1h-2ZM5 17h12v1h-12ZM19 17h2v1h-2ZM5 18h12v1h-12ZM19 18h2v1h-2ZM4 19h16v1h-16ZM4 20h16v1h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"},
		{Fill: "#1c1c1c", D: "M5 7h2v1h-2ZM15 7h2v1h-2ZM5 8h2v1h-2ZM15 8h2v1h-2ZM5 9h2v1h-2ZM15 9h2v1h-2Z"},
		{Fill: "#ff8700", D: "M10 11h2v1h-2Z"},
	}
	got := Paths(Ready, 0, 0, 1, nil)
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Fill != want[i].Fill {
			t.Errorf("path %d fill: got %s, want %s", i, got[i].Fill, want[i].Fill)
		}
		if got[i].D != want[i].D {
			t.Errorf("path %d data differs from the hand-drawn mark:\n got %s\nwant %s",
				i, got[i].D, want[i].D)
		}
	}
}

// Each wag position has to cover the same cells, or the tail changes weight as
// it moves and reads as a flicker rather than a wag.
func TestWagPositionsAreTheSameCellCount(t *testing.T) {
	n := len(Tails[WagCycle[0]])
	for _, pose := range WagCycle {
		if got := len(Tails[pose]); got != n {
			t.Errorf("tail %q has %d cells, %q has %d", pose, got, WagCycle[0], n)
		}
	}
}

// The animated mark and the demo film both split the cat in two: the chest and
// the head breathe, the haunches and the paws stay on the ground. That split
// only holds because rows 8, 9 and 10 are the same full-width row — the planted
// half is drawn from row 8, so a rising chest reveals a spare row rather than
// the background. Reshape the body through here and the mark tears open across
// its middle on every inhale, which is not something a test of the finished SVG
// would notice.
func TestTheRowsTheBreathSplitsOnAreInterchangeable(t *testing.T) {
	for _, r := range []int{9, 10} {
		if Body[r] != Body[8] {
			t.Errorf("row %d is %q but row 8 is %q: the chest can no longer rise without tearing",
				r, Body[r], Body[8])
		}
	}
}
