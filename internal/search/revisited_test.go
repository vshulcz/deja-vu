package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session that says one thing and later says the opposite is served by the
// passage that matched hardest, which is the argument rather than the
// conclusion. Recall answered "we are not on ClawHub yet" out of a session
// whose later half records the submission going through, and the reader acted
// on the stale one (#2976).
func TestAHitSaysWhenTheSessionCameBackToIt(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(27),
		Messages: []model.Message{
			// Three passages the ranking prefers — the excerpt budget is three,
			// which is what leaves the session's last word out of the answer.
			{Role: "user", Text: "clawhub clawhub clawhub — checked, we are not on clawhub", Time: day(20)},
			{Role: "user", Text: "clawhub clawhub again, still not there", Time: day(21)},
			{Role: "assistant", Text: "clawhub clawhub, nothing under our name", Time: day(22)},
			{Role: "user", Text: strings.Repeat("filler ", 200), Time: day(24)},
			// The later word on it, matching once.
			{Role: "assistant", Text: "the clawhub listing is live now, moderation passed", Time: day(27)},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "clawhub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hit, so nothing was measured")
	}
	h := hits[0]
	if h.Revisited != "2026-08-27" {
		t.Fatalf("Revisited = %q, want the date of the later mention", h.Revisited)
	}
	var out bytes.Buffer
	Print(&out, hits, Options{Query: "clawhub", Width: 120})
	if !strings.Contains(out.String(), "comes back to this later (2026-08-27)") {
		t.Fatalf("the answer does not say the session revisits this:\n%s", out.String())
	}
}

// And it stays quiet when the excerpts already carry the session's last word —
// a note on every hit is a note nobody reads.
func TestAHitIsQuietWhenTheExcerptsAreTheLastWord(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(21),
		Messages: []model.Message{
			{Role: "user", Text: "clawhub publish, first look", Time: day(20)},
			{Role: "assistant", Text: "clawhub listing is live", Time: day(21)},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "clawhub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hit, so nothing was measured")
	}
	if hits[0].Revisited != "" {
		t.Fatalf("Revisited = %q on a session whose last mention is shown", hits[0].Revisited)
	}
	var out bytes.Buffer
	Print(&out, hits, Options{Query: "clawhub", Width: 120})
	if strings.Contains(out.String(), "comes back to this later") {
		t.Fatalf("the note was printed anyway:\n%s", out.String())
	}
}

// Only the messages that matched count as coming back to it: a session that
// went on to talk about something else has not revisited the query.
func TestLaterTalkAboutSomethingElseIsNotARevisit(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 8, n, 9, 0, 0, 0, time.Local) }
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Started: day(20), Updated: day(28),
		Messages: []model.Message{
			{Role: "user", Text: "clawhub publish, checked, we are not there", Time: day(20)},
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
	if hits[0].Revisited != "" {
		t.Fatalf("Revisited = %q, but the later message is about something else", hits[0].Revisited)
	}
}
