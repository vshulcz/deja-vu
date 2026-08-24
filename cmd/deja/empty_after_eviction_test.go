package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "nothing to index yet" is the sentence for a machine deja has never had
// history from. Printed after a store went away it contradicts the line above
// it, which has just said what was lost: the reader is told they are new here
// and that N indexed files are gone, in that order (#1762).
func TestTheEmptyIndexSentenceKnowsSomethingWasLost(t *testing.T) {
	fresh := emptyIndexReason(index.BuildSummary{Initial: true}, 0)
	if !strings.Contains(fresh, "nothing to index yet") {
		t.Errorf("a first run says %q", fresh)
	}

	lost := emptyIndexReason(index.BuildSummary{}, 3)
	if strings.Contains(lost, "yet") {
		t.Errorf("a run that evicted a store still reads as a first run: %q", lost)
	}
	if !strings.Contains(lost, "3") {
		t.Errorf("the sentence does not say how much went away: %q", lost)
	}
	if !strings.Contains(lost, "gone") && !strings.Contains(lost, "went away") {
		t.Errorf("the sentence does not say what happened: %q", lost)
	}

	// One file reads as one file.
	if one := emptyIndexReason(index.BuildSummary{}, 1); strings.Contains(one, "1 indexed files") {
		t.Errorf("plural for a single file: %q", one)
	}
}

// Records kept back because their volume is merely unmounted have not gone
// away, so the counter must not see them — otherwise a reconnectable disk reads
// as history lost.
func TestUnmountedRecordsAreNotCountedAsGone(t *testing.T) {
	if n := index.ReportEvictedFiles(); n != 0 {
		t.Fatalf("the counter starts at %d", n)
	}
	// Two reads in a row: the first clears it, so a later command in the same
	// process cannot inherit a count that was already spent.
	if n := index.ReportEvictedFiles(); n != 0 {
		t.Errorf("the counter did not clear: %d", n)
	}
}
