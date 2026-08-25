package main

import (
	"strings"
	"testing"
	"time"
)

// The memory half is the one withFileMemory says has to survive, so it is
// fitted before any of the usage numbers are appended. Only the title was
// elastic: the filename was fixed at 28 columns, so a long path put the segment
// past the bar however far the title had been cut, and a bar has no horizontal
// scroll (#1880, the shape of #1076).
func TestTheMemorySegmentFitsWhereTheFloorsAllowIt(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	long := fileMemory{Path: "/p/" + strings.Repeat("n", 60) + ".go", Sessions: 3, Title: "retry", Last: when}
	wide := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: strings.Repeat("計", 40), Last: when}
	for _, c := range []struct {
		name  string
		mem   fileMemory
		width int
	}{
		{"long name, short title", long, 60},
		{"long name, long title", fileMemory{Path: long.Path, Sessions: 3, Title: strings.Repeat("word ", 12), Last: when}, 60},
		{"wide script title", wide, 60},
	} {
		if got := memorySegment(c.mem, c.width); barColumns(got) > c.width {
			t.Errorf("%s: %d columns on a %d-column bar: %q", c.name, barColumns(got), c.width, got)
		}
	}
}

// Below both floors the segment is still over-width, and that is the older
// decision: a title cut to a stub in quotation marks says less than the count
// form, which is what TestStatuslineNarrowBarKeepsAReadableTitle pins. What
// this asserts is that the filename gives way as far as it may, so the segment
// is no wider than the floors require.
func TestOnATooNarrowBarTheFilenameStillGivesWay(t *testing.T) {
	m := fileMemory{
		Path:     "/p/" + strings.Repeat("n", 60) + ".go",
		Sessions: 3,
		Title:    "retry",
		Last:     time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
	}
	got := memorySegment(m, 40)
	fixedName := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMaxName)
	if barColumns(got) >= barColumns(fixedName) {
		t.Errorf("the filename did not give way: %q is not narrower than %q", got, fixedName)
	}
	// The title floor still holds, so the segment says what the session was
	// about rather than showing a stub.
	open, close := strings.Index(got, "“"), strings.Index(got, "”")
	if open < 0 || close < open {
		t.Errorf("the quoted title is gone: %q", got)
	}
}

// An ordinary bar is unchanged: the fitting only takes away when it has to.
func TestAnOrdinaryMemorySegmentIsNotTrimmed(t *testing.T) {
	m := fileMemory{Path: "/p/parser.go", Sessions: 3, Title: "retry loop", Last: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}
	got := memorySegment(m, 80)
	if !strings.Contains(got, "parser.go") || !strings.Contains(got, "retry loop") {
		t.Errorf("a segment that fits was trimmed anyway: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("a segment that fits carries an ellipsis: %q", got)
	}
}

// The budgets are columns of allowance, not columns of line: a title shorter
// than its budget gives nothing back when the budget is cut. Subtracting the
// overflow in one step therefore left bars over-width while both parts were
// still above their floors — 57 of 2496 measured. This walks the grid and
// allows an over-width segment only where even both floors would not fit.
func TestNoBarIsLeftOverWidthWhileThePartsCanStillGiveWay(t *testing.T) {
	when := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	titles := []string{"retry", strings.Repeat("word ", 12), strings.Repeat("計", 30)}
	for width := 20; width <= 120; width += 4 {
		for nameLen := 5; nameLen <= 80; nameLen += 5 {
			for _, title := range titles {
				m := fileMemory{Path: "/p/" + strings.Repeat("n", nameLen) + ".go", Sessions: 3, Title: title, Last: when}
				got := memorySegment(m, width)
				if barColumns(got) <= width {
					continue
				}
				atFloor := "deja · " + statuslineMemoryLineWithin(m, statuslineMinTitle, statuslineMinName)
				if barColumns(atFloor) <= width {
					t.Fatalf("width=%d name=%d: %d columns, though the floors fit in %d: %q",
						width, nameLen, barColumns(got), barColumns(atFloor), got)
				}
			}
		}
	}
}
