package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Every line the brief prints has to fit the width it laid out for. The
// exception is the `recent` block, where a prefix that already fills the line
// keeps a 12-column title rather than an empty one — that floor is deliberate
// and TestBriefRecentLinesHonourColumns pins it.
//
// Measured before the fix on a 1548-session store: 5 lines over at COLUMNS=60,
// 9 at 50, 12 at 40. `today`, `before` and `try` carried no budget at all, and
// the fixed-prefix lines were cut to a constant 44 columns — 56 with the label,
// wider than the pane (#1588).
func TestBriefLinesFitNarrowTerminals(t *testing.T) {
	day := 24 * time.Hour
	// A project path of the length people actually have, and recalls served
	// today: the `today` line only grows past the edge once it carries the two
	// sizes, and `before` only once the project name is long.
	dir := briefStore(t, "work/platform/services/session-indexer", 12*day, 15*day, 19*day)
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 4096), 3, 262144)
	usage.RecordDigest(dir, usage.KindDejaVu, "dv digest", 2, 4096)

	// 60 is the width the code lays out for — briefTitleBudget names the
	// 60-column split pane #604 fixed — and 80 is the default. Below 60 the
	// screen still overflows on the header, the suggestion and the `recent`
	// floor; those are separate items, not this one.
	for _, width := range []int{60, 80} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		var buf bytes.Buffer
		if err := runBrief(dir, &buf); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			text := visibleText(line)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if isBriefRecentLine(text) {
				continue // the documented floor
			}
			// The fixed instruction lines fit 60 columns; cutting them with an
			// ellipsis would hide the command they exist to give, so they are
			// not budgeted at all (#1588).
			if w := termwidth.Columns(text); w > width {
				t.Errorf("COLUMNS=%d: %d columns: %q", width, w, text)
			}
		}
	}
}

func isBriefRecentLine(text string) bool {
	return strings.HasPrefix(text, "recent ") || (strings.HasPrefix(text, "  ") && strings.Contains(text, "] "))
}
