package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An install prints two counts in a row and they disagree: the parser's line
// says what it read, this one says what reached the index, and the difference
// is deja's own recall coming back through a transcript. Both are true, and
// nothing said why they differed (#3386).
func TestTheBuiltLineNamesWhatDidNotReachTheIndex(t *testing.T) {
	got := indexBuiltLine(index.BuildSummary{Sessions: 4, Messages: 6, Dropped: 1})
	if !strings.Contains(got, "4 sessions, 6 messages") {
		t.Errorf("the line lost its counts: %q", got)
	}
	if !strings.Contains(got, "1 of deja's own blocks not indexed") {
		t.Errorf("the line does not say where the seventh message went: %q", got)
	}

	// When the two agree there is nothing to explain, and a note about zero
	// blocks is noise on the screen a first install shows.
	if got := indexBuiltLine(index.BuildSummary{Sessions: 4, Messages: 7}); strings.Contains(got, "not indexed") {
		t.Errorf("the line explained a difference that is not there: %q", got)
	}
}
