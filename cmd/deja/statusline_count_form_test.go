package main

import (
	"strings"
	"testing"
	"time"
)

// `statuslineMinTitle`'s comment says what happens below the floor — "the count
// form says more than three words and an ellipsis" — and nothing reached it:
// memorySegment never passed a budget that low, so a bar too narrow for the
// floored title ran off the edge instead of using the shorter sentence (#1903).
func TestANarrowBarTakesTheCountFormWhenItFits(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser", Last: when}

	floored := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMinName)
	count := "deja · " + statuslineMemoryLineWithin(m, 0, statuslineMinName)
	if barColumns(count) >= barColumns(floored) {
		t.Fatalf("this memory does not shorten in the count form (%d against %d), so the test says nothing",
			barColumns(count), barColumns(floored))
	}

	// A bar between the two: the count form fits, the title form does not.
	width := barColumns(count)
	got := memorySegment(m, width)
	if barColumns(got) > width {
		t.Errorf("%d columns on a %d-column bar: %q", barColumns(got), width, got)
	}
	if strings.Contains(got, "“") {
		t.Errorf("the title form was kept though it does not fit: %q", got)
	}
	if !strings.Contains(got, "3 earlier sessions") {
		t.Errorf("the count form is not what was printed: %q", got)
	}
}

// Where the title fits, it stays: a count is a statistic, the title is the
// memory.
func TestAWideBarKeepsTheTitle(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser",
		Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 80)
	if !strings.Contains(got, "retry loop") {
		t.Errorf("a bar with room lost the title: %q", got)
	}
}

// And where neither form fits, the title stays rather than being traded for a
// sentence that does not fit either — the decision
// TestStatuslineNarrowBarKeepsAReadableTitle pins.
func TestWhenNothingFitsTheTitleSurvives(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop in the parser",
		Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 20)
	if !strings.Contains(got, "“") {
		t.Errorf("a bar too narrow for either form dropped the title: %q", got)
	}
}
