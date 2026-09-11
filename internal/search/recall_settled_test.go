package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The block handed over at session start served the newest sessions, and on the
// context benchmark four of its six slots carried "routine update, nothing
// settled here" while the session holding how the error was fixed never got
// one — a third of that corpus's facts, present in the per-hit digest and
// missing from the block. A session that settled something now beats one that
// did not, whatever their dates.
func TestTheStartBlockPrefersSessionsThatSettledSomething(t *testing.T) {
	now := time.Now()
	var ss []model.Session
	// Newest first: four that settled nothing, then the two that did.
	for i := range 4 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("idle-%d", i), Project: "org/app",
			Updated: now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "routine update on the etag cache, nothing settled here"},
				{Role: "assistant", Text: "looked at the etag cache and the refresh path, still reading"},
			},
		})
	}
	ss = append(ss, model.Session{
		Harness: "claude", ID: "decided", Project: "org/app",
		Updated: now.Add(-10 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "what do we do about the etag cache refresh"},
			{Role: "assistant", Text: "we decided to use bounded refresh with jitter because retries must spread load"},
		},
	})
	ss = append(ss, model.Session{
		Harness: "claude", ID: "fixed", Project: "org/app",
		Updated: now.Add(-20 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "the etag reuse keeps serving stale bodies"},
			{Role: "assistant", Text: "fixed it by replacing stale etag reuse with generation checks"},
		},
	})

	got := BuildAutoRecall(ss, AutoRecallOptions{
		Mode: RecallSafe, ProjectNames: []string{"org/app"}, Now: now,
	})
	if got.Sessions == 0 {
		t.Fatal("the digest served nothing")
	}
	for _, want := range []string{"bounded refresh with jitter", "replacing stale etag reuse"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the block dropped what was settled (%q):\n%s", want, got.Text)
		}
	}
	// And the sessions it led with are those two, not the four newer ones.
	if len(got.IDs) < 2 || (got.IDs[0] != "decided" && got.IDs[0] != "fixed") {
		t.Errorf("the block opened on %v, not on a session that settled something", got.IDs)
	}
}
