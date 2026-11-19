package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"testing"
)

func TestANoteIsLiftedAboveAnImportedSource(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}},
		{Session: model.Session{ID: "imported-def", OrigID: "sess1", Harness: "claude"}},
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}},
	}
	liftNotesAboveTheirSource(hits)
	if got := liftIDs(hits); got[1] != "deja-note-claude-sess1" {
		t.Errorf("a note about an imported source was not lifted: %v", got)
	}
}

// Both at once, which is what a synced pair actually looks like.
func TestAnImportedNoteIsLiftedAboveAnImportedSource(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "imported-def", OrigID: "sess1", Harness: "claude"}},
		{Session: model.Session{ID: "imported-abc", OrigID: "deja-note-claude-sess1", Harness: "deja"}},
	}
	liftNotesAboveTheirSource(hits)
	if got := liftIDs(hits); got[0] != "imported-abc" {
		t.Errorf("a synced pair was left in source-first order: %v", got)
	}
}

// The same rule for a caller that ranks sessions rather than hits.
func TestAnImportedNoteSessionIsLiftedAboveItsSource(t *testing.T) {
	ss := []model.Session{
		{ID: "sess1", Harness: "claude"},
		{ID: "imported-abc", OrigID: "deja-note-claude-sess1", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(ss)
	if got := sessionIDs(ss); got[0] != "imported-abc" {
		t.Errorf("an imported note was not lifted on the session path: %v", got)
	}
}

func sessionIDs(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// The per-prompt hook ranks sessions and never builds a Hit, so the rule the
// sort carries could not reach it: the transcript a note was distilled from
// took a slot in the block while the note itself waited behind it (#2803).
func TestNoteSessionsAreLiftedAboveTheirSource(t *testing.T) {
	ss := []model.Session{
		{ID: "other", Harness: "claude"},
		{ID: "sess1", Harness: "claude"},
		{ID: "unrelated", Harness: "claude"},
		{ID: "deja-note-claude-sess1", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(ss)
	want := []string{"other", "deja-note-claude-sess1", "sess1", "unrelated"}
	got := sessionIDs(ss)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("note not lifted in front of its source: got %v want %v", got, want)
		}
	}
}

// A note already ahead of its source stays put, a note whose source is absent
// is left alone, and a harness whose name carries a dash does not split wrong.
func TestNoteSessionLiftLeavesEverythingElseAlone(t *testing.T) {
	ss := []model.Session{
		{ID: "deja-note-claude-sess1", Harness: "deja"},
		{ID: "sess1", Harness: "claude"},
		{ID: "deja-note-claude-gone", Harness: "deja"},
		{ID: "deja-2026-01-02-proj", Harness: "deja"},
	}
	before := sessionIDs(ss)
	LiftNoteSessionsAboveTheirSource(ss)
	after := sessionIDs(ss)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("order changed with nothing to lift: %v -> %v", before, after)
		}
	}

	dashed := []model.Session{
		{ID: "one", Harness: "codex-history"},
		{ID: "deja-note-codex-history-one", Harness: "deja"},
	}
	LiftNoteSessionsAboveTheirSource(dashed)
	if got := sessionIDs(dashed); got[0] != "deja-note-codex-history-one" {
		t.Errorf("a harness name with a dash was not matched: %v", got)
	}

	LiftNoteSessionsAboveTheirSource(nil)
}

func TestLiftNoteSessionsAlignedKeepsParallelMetadataSynced(t *testing.T) {
	ss := []model.Session{
		{ID: "other", Harness: "claude"},
		{ID: "sess1", Harness: "claude"},
		{ID: "unrelated", Harness: "claude"},
		{ID: "deja-note-claude-sess1", Harness: "deja"},
	}
	matched := []int{10, 20, 30, 40}
	strong := []int{1, 2, 3, 4}
	naming := []int{0, 0, 0, 1}
	LiftNoteSessionsAligned(ss, matched, strong, naming)
	wantIDs := []string{"other", "deja-note-claude-sess1", "sess1", "unrelated"}
	wantMatched := []int{10, 40, 20, 30}
	wantStrong := []int{1, 4, 2, 3}
	wantNaming := []int{0, 1, 0, 0}
	for i := range ss {
		if ss[i].ID != wantIDs[i] || matched[i] != wantMatched[i] || strong[i] != wantStrong[i] || naming[i] != wantNaming[i] {
			t.Fatalf("misaligned at %d: id=%q m=%d s=%d n=%v", i, ss[i].ID, matched[i], strong[i], naming[i])
		}
	}
}

func liftIDs(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Session.ID)
	}
	return out
}

// A promoted note carries one distilled line; the transcript it came from
// carries the query words in its title and repeats them throughout. On score
// alone the transcript wins its own distillation, which is the opposite of
// what `promote` tells the user it just did.
func TestPromotedNoteIsLiftedAboveItsSourceTranscript(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "other", Harness: "claude"}, Score: 9},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 5},
		{Session: model.Session{ID: "unrelated", Harness: "claude"}, Score: 3},
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}, Score: 1},
	}
	liftNotesAboveTheirSource(hits)
	got := liftIDs(hits)
	want := []string{"other", "deja-note-claude-sess1", "sess1", "unrelated"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("note not lifted in front of its source: got %v want %v", got, want)
		}
	}
}

// A note already ahead of its source stays put, and a note whose source is not
// in the results is left alone — the rule reorders one pair, nothing else.
func TestNoteLiftLeavesUnrelatedOrderAlone(t *testing.T) {
	hits := []Hit{
		{Session: model.Session{ID: "deja-note-claude-sess1", Harness: "deja"}, Score: 9},
		{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 5},
		{Session: model.Session{ID: "deja-note-claude-gone", Harness: "deja"}, Score: 4},
		{Session: model.Session{ID: "deja-2026-01-02-proj", Harness: "deja"}, Score: 2},
	}
	before := liftIDs(hits)
	liftNotesAboveTheirSource(hits)
	after := liftIDs(hits)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("order changed with nothing to lift: %v -> %v", before, after)
		}
	}

	none := []Hit{{Session: model.Session{ID: "sess1", Harness: "claude"}, Score: 1}}
	liftNotesAboveTheirSource(none)
	if none[0].Session.ID != "sess1" {
		t.Fatalf("no notes present, nothing to do: %v", liftIDs(none))
	}
}

// The note-over-source rule lived in the sort, so it reached the tiers that go
// through it and missed the two that arrive already ranked: a pasted error and
// the "ranked by relevance" screen served a note behind the transcript it was
// distilled from — the one ordering `promote` prints a promise about (#2803).
func TestTheErrorAndRelevanceTiersLiftANoteOverItsSource(t *testing.T) {
	note := model.Session{ID: "deja-note-claude-sess1", Harness: "deja",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer pool cap settled at 40"}}}
	source := model.Session{ID: "sess1", Harness: "claude",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer pool cap settled at 40 after a long argument"}}}
	other := model.Session{ID: "other", Harness: "claude",
		Messages: []model.Message{{Role: "user", Text: "pgbouncer restarts on deploy"}}}

	// Ranked with the source ahead of its own note, which is the order the
	// callers of these two builders hand in.
	ranked := []model.Session{other, source, note}

	for _, tc := range []struct {
		name string
		hits []Hit
	}{
		{"error", ErrorHits(ranked, "")},
		{"relevance", RelevanceHitsWeighted(ranked, []string{"pgbouncer", "pool"}, nil)},
	} {
		var at = map[string]int{}
		for i, h := range tc.hits {
			at[h.Session.ID] = i
		}
		if at["deja-note-claude-sess1"] > at["sess1"] {
			t.Errorf("%s: the note is behind the transcript it was promoted from: %v", tc.name, liftIDs(tc.hits))
		}
		// The scores have to agree with the order, or a caller that re-sorts
		// reads the old one back.
		for i := 1; i < len(tc.hits); i++ {
			if tc.hits[i-1].Score < tc.hits[i].Score {
				t.Errorf("%s: score disagrees with position at %d: %v", tc.name, i, liftIDs(tc.hits))
			}
		}
		// And nothing else moved: the rule reorders one pair.
		if at["other"] != 0 {
			t.Errorf("%s: an unrelated session was moved: %v", tc.name, liftIDs(tc.hits))
		}
	}
}
