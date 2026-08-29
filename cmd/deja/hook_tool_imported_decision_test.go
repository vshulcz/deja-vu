package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A decision promoted on another machine arrives as a session carrying its
// state, which is how #2421 taught the view page to read it. The line before an
// edit read this machine's notes file alone, so on the receiving machine the
// decision was a decision in search and filler prose at the moment it mattered
// (#2510).
func TestTheEditLineReadsADecisionThatArrivedBySync(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	metas := []index.SessionMeta{
		{ID: "imported-9", Harness: "claude", Project: "imported:home/app", Updated: now.Add(-time.Hour),
			Title: "should the retry budget go up to 10 for the payments client?",
			// What sync carries for a promoted session.
			Lifecycle:     "accepted",
			LifecycleNote: "the retry budget stays at 5; the pool change is what fixed the timeouts",
			LifecycleAt:   now.Add(-time.Hour).Format(time.RFC3339),
		},
		{ID: "b1", Harness: "claude", Project: "home/app", Updated: now},
	}

	got := promotedDecisionFor(metas)
	if !strings.Contains(got, "retry budget stays at 5") {
		t.Errorf("the imported decision is not what the line carries: %q", got)
	}
}

// A state that is not "accepted" is not a standing decision, wherever it came
// from: rejected, superseded and stale all mean someone took it back.
func TestASupersededImportedDecisionIsNotCarried(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	metas := []index.SessionMeta{
		{ID: "imported-9", Harness: "claude", Project: "imported:home/app", Updated: now,
			Title: "the old plan", Lifecycle: "superseded", LifecycleNote: "we went back to five",
			LifecycleAt: now.Format(time.RFC3339)},
	}
	if got := promotedDecisionFor(metas); got != "" {
		t.Errorf("a decision that was taken back is still being served: %q", got)
	}
}

// The trust rule reaches it too: an imported project the auto activation
// withholds must not speak here.
func TestAWithheldImportedDecisionStaysOut(t *testing.T) {
	hermeticEnv(t)
	writePolicy(t, `{"activations":{"auto":{"imported":false}}}`)
	now := time.Now().UTC()
	metas := []index.SessionMeta{
		{ID: "imported-9", Harness: "claude", Project: "imported:home/app", Updated: now,
			Title: "t", Lifecycle: "accepted", LifecycleNote: "the retry budget stays at 5",
			LifecycleAt: now.Format(time.RFC3339)},
	}
	if got := promotedDecisionFor(metas); got != "" {
		t.Errorf("an imported decision the rule withholds reached the line: %q", got)
	}
}
