package search

import (
	"testing"
	"time"
)

// The age column is the one part of the screen meant to be read vertically.
// Two sessions a day apart printed "6d ago" and "Jul 26" one row under the
// other, because the relative form stops at a week — correct on each row and
// unreadable as a column.
func TestDateColumnHoldsOneFormForTheWholeList(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour

	// Everything inside the week: the relative form, which is the warm one.
	recent := []time.Time{now, now.Add(-2 * day), now.Add(-6 * day)}
	fmtRecent := dateColumn(recent)
	if got := fmtRecent(recent[0]); got != "today" {
		t.Errorf("newest row = %q, want the relative form", got)
	}
	if got := fmtRecent(recent[2]); got != "6d ago" {
		t.Errorf("oldest row = %q, want the relative form", got)
	}

	// One row past the week takes the whole column with it.
	mixed := []time.Time{now, now.Add(-6 * day), now.Add(-8 * day)}
	fmtMixed := dateColumn(mixed)
	for i, when := range mixed {
		got := fmtMixed(when)
		if got == "today" || len(got) > 0 && got[len(got)-1] == 'o' {
			t.Errorf("row %d = %q; one row aged out, so no row may be relative", i, got)
		}
	}
	// And consecutive days now read as consecutive.
	first, second := fmtMixed(mixed[1]), fmtMixed(mixed[2])
	if first == second {
		t.Errorf("two days rendered the same: %q", first)
	}
}

// A zero time is a session with no date; it must not decide the column's form
// for the rows that do have one.
func TestDateColumnIgnoresUndatedRows(t *testing.T) {
	now := time.Now()
	col := dateColumn([]time.Time{{}, now, now.Add(-time.Hour)})
	if got := col(now); got != "today" {
		t.Errorf("column = %q, want the relative form; an undated row is not an old one", got)
	}
}
