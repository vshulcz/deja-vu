package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestIsLocalProject(t *testing.T) {
	for _, c := range []struct {
		project string
		want    bool
	}{
		{"myproj/99", true},
		{"", true},
		{"imported:peerproj/9", false},
		{"imported:", false},
		// Not the prefix: a local project may legitimately be named after the
		// word.
		{"imported-notes", true},
		{"my/imported:thing", true},
	} {
		if got := isLocalProject(c.project); got != c.want {
			t.Errorf("isLocalProject(%q) = %v", c.project, got)
		}
	}
}

// Same evidence, same moment: the tie used to fall to the id, and
// "imported-…" sorts ahead of most session ids — so a peer's copy outranked
// the local session for no reason anyone chose (#711).
func TestEqualHitsPreferTheLocalSession(t *testing.T) {
	at := time.Date(2026, 8, 1, 5, 42, 1, 0, time.UTC)
	hits := []Hit{
		{Session: model.Session{ID: "imported-19c23b9cfc52", Project: "imported:peerproj/9", Updated: at}, Score: 3},
		{Session: model.Session{ID: "tie-local", Project: "myproj/99", Updated: at}, Score: 3},
	}
	sortHits(hits)
	got := hits
	if got[0].Session.ID != "tie-local" {
		t.Errorf("order = %q, %q", got[0].Session.ID, got[1].Session.ID)
	}

	// Reversed input, same answer.
	hits = []Hit{hits[1], hits[0]}
	sortHits(hits)
	if got := hits; got[0].Session.ID != "tie-local" {
		t.Errorf("reversed order = %q", got[0].Session.ID)
	}

	// Two local sessions still fall back to the id, and a better score still
	// wins outright over locality.
	hits = []Hit{
		{Session: model.Session{ID: "b", Project: "p", Updated: at}, Score: 3},
		{Session: model.Session{ID: "a", Project: "p", Updated: at}, Score: 3},
	}
	sortHits(hits)
	if got := hits; got[0].Session.ID != "a" {
		t.Errorf("two local: %q", got[0].Session.ID)
	}
	hits = []Hit{
		{Session: model.Session{ID: "local", Project: "p", Updated: at}, Score: 1},
		{Session: model.Session{ID: "peer", Project: "imported:x/y", Updated: at}, Score: 9},
	}
	sortHits(hits)
	if got := hits; got[0].Session.ID != "peer" {
		t.Errorf("score must win over locality: %q", got[0].Session.ID)
	}
}
