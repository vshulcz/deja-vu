package search

import "testing"

// The line says what happened rather than naming the state, so a hit that was
// taken back reads as taken back in blame too (#1019).
func TestBlameLifecycleLineWordsWhatHappened(t *testing.T) {
	for _, tc := range []struct {
		name string
		hit  BlameHit
		want string
	}{
		{"none", BlameHit{}, ""},
		{"rejected", BlameHit{Lifecycle: "rejected"}, "this was tried and rejected"},
		{"superseded", BlameHit{Lifecycle: "superseded"}, "a later decision replaced this"},
		{"stale", BlameHit{Lifecycle: "stale"}, "marked stale — may no longer hold"},
		{"unknown state passes through", BlameHit{Lifecycle: "parked"}, "parked"},
		{
			"with date and note",
			BlameHit{Lifecycle: "rejected", LifecycleAt: "2026-08-01", LifecycleNote: "deadlocked under load"},
			"this was tried and rejected (2026-08-01): deadlocked under load",
		},
		{
			"date without note",
			BlameHit{Lifecycle: "stale", LifecycleAt: "2026-07-04"},
			"marked stale — may no longer hold (2026-07-04)",
		},
	} {
		if got := BlameLifecycleLine(tc.hit); got != tc.want {
			t.Errorf("%s: line = %q, want %q", tc.name, got, tc.want)
		}
	}
}
