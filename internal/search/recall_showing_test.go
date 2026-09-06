package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The session the digest shows first is the one a citation must name. A
// top-ranked session with nothing quotable is skipped by the digest, and a
// citation taken from the ranking then pointed at a session the agent never
// saw.
func TestDigestSaysWhichSessionsItShowed(t *testing.T) {
	now := time.Now()
	empty := model.Session{ID: "top", Harness: "claude", Updated: now}
	real := model.Session{ID: "shown", Harness: "claude", Updated: now, Messages: []model.Message{
		{Role: "user", Text: "why does the reconciler double count refunds", Time: now},
		{Role: "assistant", Text: "the reconciler double counts refunds because the cursor is reset per page", Time: now},
	}}
	text, shown := AutoRecallDigestShowing([]model.Session{empty, real}, 0, []string{"reconciler", "refunds"}, "")
	if text == "" {
		t.Fatal("digest empty")
	}
	if len(shown) != 1 || shown[0].ID != "shown" {
		t.Fatalf("shown = %+v, want only the session with a quotable line", ids(shown))
	}
}

func ids(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
