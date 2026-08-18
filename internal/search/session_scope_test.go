package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func twoSessionsWithWork(now time.Time) []model.Session {
	return []model.Session{
		{
			Harness: "grok", Project: "app", ID: "01a00feb", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: "why does the build fail"},
				{Role: "tool-output", Text: "go build ./... : undefined parseThing"},
			},
		},
		{
			Harness: "grok", Project: "app", ID: "77bb2211", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: "why does the build fail here too"},
				{Role: "tool-output", Text: "go build ./... : undefined otherThing"},
			},
		},
	}
}

// Finding a session by what was said and then searching inside it for what was
// done is the second step deja had no way to take: the id had to be turned back
// into a file path and grepped by hand (#1321).
func TestSearchNarrowsToOneSession(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Role: "tool", Session: "01a00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want only the named session: %#v", len(hits), hits)
	}
	if hits[0].Session.ID != "01a00feb" {
		t.Errorf("hit is session %s", hits[0].Session.ID)
	}
}

// Without the flag both sessions answer, so the test above is measuring the
// flag rather than a fixture that only had one match.
func TestSearchWithoutSessionSeesBoth(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Role: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want both: %#v", len(hits), hits)
	}
}

// An id that names nothing answers with nothing rather than falling back to the
// whole store.
func TestSearchWithAnUnknownSessionFindsNothing(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Session: "nosuchid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits for an id that names no session", len(hits))
	}
}

// The id a session was synced under resolves too, the way show and ctx take it
// (#1316).
func TestSearchNarrowsByTheIdASessionCameWith(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	ss[0].ID = "imported-9f3c"
	ss[0].OrigID = "01a00feb"
	hits, err := Run(ss, Options{Query: "build", All: true, Session: "01a00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "imported-9f3c" {
		t.Errorf("got %d hits: %#v", len(hits), hits)
	}
}
