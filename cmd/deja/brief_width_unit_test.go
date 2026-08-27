package main

import "testing"

// The width guards on the recent block ask this helper how much of the
// terminal a line took. A CJK character is one rune and two columns, so a
// helper counting runes would let a line twice the terminal's width past the
// guards that exist to stop exactly that (#2130).
func TestVisibleWidthCountsColumnsNotRunes(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"recent     [claude] tmp/projc · today · a title", 47},
		{"\x1b[2m[claude]\x1b[0m tmp/projc", 18},
		{"消费者重平衡", 12},
		{"recent     [claude] …者重平衡 · today", 37},
		// The numbers are written out rather than derived: a table that asks
		// the helper's own body what to expect agrees with it whatever it says.
	} {
		if got := visibleWidth(tc.line); got != tc.want {
			t.Errorf("visibleWidth(%q) = %d, want %d columns", tc.line, got, tc.want)
		}
	}
}
