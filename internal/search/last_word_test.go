package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The excerpts are the passages that matched hardest, and twice a reader acted
// on the wrong half of a session because the passage that answered was a
// different one: "we are not on ClawHub yet" from a session that later records
// the submission going through (#2976), and the goose PR reopened because the
// line saying it had already been opened and closed sat elsewhere in the same
// session (#3007). The third excerpt is the session's last word on the query.
func TestTheThirdExcerptIsTheSessionsLastWord(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(27),
		Messages: []model.Message{
			{Role: "user", Text: "clawhub clawhub clawhub — checked, we are not listed", Time: day(20)},
			{Role: "user", Text: "clawhub clawhub, still nothing under our name", Time: day(21)},
			{Role: "assistant", Text: "clawhub clawhub, no listing yet", Time: day(22)},
			{Role: "user", Text: strings.Repeat("filler ", 200), Time: day(24)},
			{Role: "assistant", Text: "the clawhub listing is live, moderation passed", Time: day(27)},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "clawhub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) != 3 {
		t.Fatalf("want three excerpts, got %#v", hits)
	}
	joined := strings.Join(hits[0].Snippets, "\n")
	if !strings.Contains(joined, "moderation passed") {
		t.Fatalf("the session's last word on the query is not shown:\n%s", joined)
	}
	// The two strongest passages still lead: the last word takes the third
	// slot, not the first.
	if !strings.Contains(hits[0].Snippets[0], "we are not listed") {
		t.Fatalf("the strongest passage lost its place:\n%s", hits[0].Snippets[0])
	}
	if !strings.Contains(hits[0].Snippets[2], "moderation passed") {
		t.Fatalf("the last word is not in the third slot:\n%s", hits[0].Snippets[2])
	}
}

// When the strongest passages already are the session's last words, nothing is
// displaced: the third slot is the third-strongest, as before.
func TestTheThirdExcerptIsUnchangedWhenTheLastWordAlreadyLeads(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(23),
		Messages: []model.Message{
			{Role: "user", Text: "clawhub, one mention", Time: day(20)},
			{Role: "user", Text: "clawhub clawhub, two", Time: day(21)},
			{Role: "assistant", Text: "clawhub clawhub clawhub, three", Time: day(23)},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "clawhub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) != 3 {
		t.Fatalf("want three excerpts, got %#v", hits)
	}
	joined := strings.Join(hits[0].Snippets, "\n")
	for _, want := range []string{"one mention", "two", "three"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q is missing from the excerpts:\n%s", want, joined)
		}
	}
}

// A session that went on to talk about something else has no later word on the
// query, and the excerpts are the three that matched.
func TestLaterTalkAboutSomethingElseDoesNotTakeASlot(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(28),
		Messages: []model.Message{
			{Role: "user", Text: "clawhub clawhub clawhub — checked, we are not there", Time: day(20)},
			{Role: "user", Text: "clawhub clawhub, still nothing", Time: day(21)},
			{Role: "assistant", Text: "clawhub, one more look", Time: day(22)},
			{Role: "assistant", Text: "moved on to the winget manifest instead", Time: day(28)},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "clawhub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hit, so nothing was measured")
	}
	joined := strings.Join(hits[0].Snippets, "\n")
	if strings.Contains(joined, "winget manifest") {
		t.Fatalf("a passage that never matched took an excerpt slot:\n%s", joined)
	}
	if !strings.Contains(joined, "one more look") {
		t.Fatalf("the third matching passage is missing:\n%s", joined)
	}
}
