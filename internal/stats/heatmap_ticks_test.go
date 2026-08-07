package stats

import (
	"testing"
	"time"
)

// Month ticks are drawn one per week column, 13px apart, and a three-letter
// label is wider than one column. Two ticks in adjacent columns overprint on
// the card, which happened whenever the trailing year opened on a partial week
// from the month before.
func TestMonthTicksNeverShareAColumnEdge(t *testing.T) {
	for i := 0; i < 400; i++ {
		now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		hm := buildHeatmap(map[string]int{}, now)
		for j := 1; j < len(hm.Months); j++ {
			if gap := hm.Months[j].Col - hm.Months[j-1].Col; gap < 2 {
				t.Fatalf("%s: %s(col %d) and %s(col %d) are %d column apart",
					now.Format("2006-01-02"), hm.Months[j-1].Label, hm.Months[j-1].Col,
					hm.Months[j].Label, hm.Months[j].Col, gap)
			}
		}
	}
}

// Dropping the crowded tick must keep the month that owns the columns after it,
// not the sliver of the month before.
func TestCrowdedTickKeepsTheMonthThatOwnsTheStrip(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	hm := buildHeatmap(map[string]int{}, now)
	if len(hm.Months) == 0 {
		t.Fatal("no month ticks")
	}
	if got := hm.Months[0]; got.Label != "Aug" || got.Col != 1 {
		t.Fatalf("first tick = %s(col %d), want Aug(col 1)", got.Label, got.Col)
	}
}
