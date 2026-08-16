package mark

import (
	"fmt"
	"strings"
	"testing"
)

// The mark was hand-drawn into four places before this package existed: the SVG
// assets, the stats card, the stats page and the local viewer. Those literals
// were replaced by Paths, and this pins the unit-scale output.
//
// The rectangles are not the ones the hand-drawn literals held: they were one
// per row, and cellsPath merges a stretch downwards through the rows that repeat
// it. The drawing is identical — what changed is how few edges it is made of,
// and every edge is somewhere a hairline of background can show through once the
// mark is scaled. TestPathsCoverEveryCellExactlyOnce is the check that survives
// the next change to how they are packed; this one is the exact expected string.
func TestPathsMatchTheHandDrawnLiterals(t *testing.T) {
	want := []Path{
		{Fill: "#8787af", D: "M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v2h-18ZM2 7h3v3h-3ZM7 7h8v3h-8ZM17 7h3v3h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v5h-2ZM5 15h12v4h-12ZM4 19h16v2h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"},
		{Fill: "#1c1c1c", D: "M5 7h2v3h-2ZM15 7h2v3h-2Z"},
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

// The paws move a cell either way, and what makes that look like paws moving
// rather than the sprite coming apart is that the haunches above them cover both
// extremes: wherever a paw lands it is still joined to the body. Narrow that row
// and a paw walks out from under the animal and stands beside it.
func TestTheHaunchesCoverThePawsWhereverTheyStep(t *testing.T) {
	above := Body[20]
	for name, paw := range Paws {
		for _, c := range paw {
			if c.Row != 21 {
				t.Fatalf("%s paw has a cell on row %d; this only reasons about the bottom row", name, c.Row)
			}
			for _, step := range []int{-1, 0, 1} {
				if col := c.Col + step; col < 0 || col >= len(above) || above[col] != '#' {
					t.Errorf("%s paw cell %d stepping %+d lands on column %d, which row 20 does not cover",
						name, c.Col, step, c.Col+step)
				}
			}
		}
	}
}

// cellsPath packs cells into as few rectangles as it can, and a packing bug is
// invisible in the finished asset until it is not: an overlap costs nothing to
// look at, and a hole is a single missing pixel somewhere in a body of eight
// hundred. Area is the invariant that catches both, and it does not care how the
// rectangles are arranged, so it outlives the exact-string test above.
func TestPathsCoverEveryCellExactlyOnce(t *testing.T) {
	for _, m := range []Mood{Ready, Surprised, Asleep, Nothing} {
		want := map[string]int{}
		for _, row := range Grid(m) {
			for _, ch := range row {
				if col, ok := Colour(byte(ch), m.CoatColour); ok {
					want[Hex[col]]++
				}
			}
		}
		for _, p := range Paths(m, 0, 0, 1, nil) {
			area := 0
			for _, rect := range strings.Split(strings.TrimSuffix(p.D, "Z"), "Z") {
				var x, y, w, h, back int
				if _, err := fmt.Sscanf(rect, "M%d %dh%dv%dh-%d", &x, &y, &w, &h, &back); err != nil {
					t.Fatalf("%s: cannot read rectangle %q: %v", p.Fill, rect, err)
				}
				if back != w {
					t.Errorf("%s: rectangle %q does not close: goes out %d and back %d", p.Fill, rect, w, back)
				}
				area += w * h
			}
			if area != want[p.Fill] {
				t.Errorf("%v %s: rectangles cover %d cells, the grid has %d", m, p.Fill, area, want[p.Fill])
			}
		}
	}
}
