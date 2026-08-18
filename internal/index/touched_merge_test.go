package index

import (
	"slices"
	"testing"
)

// SessionMeta.Touched is the files a session worked on most, and a session gets
// updated in batches — a re-index of a growing transcript, a sync from another
// machine. Filling the list from the older batch and cutting the newer one off
// threw away the ranking both sides were handed.
func TestTouchedMergeKeepsTheTopOfBothBatches(t *testing.T) {
	have := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	add := rankTouched(map[string]int{"hot.go": 40, "cold.go": 1})
	got := mergeTouched(slices.Clone(have), add)
	if !slices.Contains(got, "hot.go") {
		t.Errorf("merged %v: the file this batch worked on hardest is missing", got)
	}
	if got[0] != "a.go" {
		t.Errorf("merged %v: the file the session worked on hardest before is no longer first", got)
	}
	if len(got) > touchedFileCap {
		t.Errorf("merged %v: over the cap of %d", got, touchedFileCap)
	}
}

// Nothing may be listed twice, and a batch that adds nothing new must leave the
// list alone — otherwise re-indexing an unchanged session would churn it.
func TestTouchedMergeIsStableAndDeduplicates(t *testing.T) {
	have := []string{"a.go", "b.go", "c.go"}
	got := mergeTouched(slices.Clone(have), []string{"b.go", "a.go"})
	if !slices.Equal(got, have) {
		t.Errorf("merged %v, want %v unchanged", got, have)
	}
}

// The empty cases the callers actually hit: a session with no earlier list, and
// a batch that touched nothing.
func TestTouchedMergeHandlesEmptySides(t *testing.T) {
	if got := mergeTouched(nil, []string{"a.go", "b.go"}); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Errorf("merged %v from an empty session", got)
	}
	if got := mergeTouched([]string{"a.go"}, nil); !slices.Equal(got, []string{"a.go"}) {
		t.Errorf("merged %v from an empty batch", got)
	}
}
