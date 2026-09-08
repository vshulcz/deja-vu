package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The session-start digest opened on "продолжай": a real message a person
// typed, so none of the plumbing rules caught it, and the block then said "go
// on" and "nothing settled yet" while the session that settled the question
// came second (#3412). A line that names nothing is not a problem statement.
func TestABareContinuationIsNotTheProblemStatement(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		Harness: "claude", Project: "w/proj", ID: "live-1", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "продолжай", Time: at},
			{Role: "assistant", Text: "смотрю на батчер, пока ничего не решено", Time: at.Add(time.Minute)},
			{Role: "user", Text: "the quokkabloom batcher caps the payload at 4 KB", Time: at.Add(2 * time.Minute)},
		},
	}
	got := autoRecallSessionFor(s, at.Add(time.Hour), true, nil)
	if strings.Contains(got, "User: продолжай") {
		t.Errorf("a bare continuation is quoted as the problem:\n%s", got)
	}
	if !strings.Contains(got, "quokkabloom batcher") {
		t.Errorf("the line that does name something was not taken instead:\n%s", got)
	}
}

// And a session whose only user line names nothing still shows what it
// concluded rather than disappearing.
func TestASessionWithNoProblemLineStillShowsItsConclusion(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		Harness: "claude", Project: "w/proj", ID: "live-2", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "давай дальше", Time: at},
			{Role: "assistant", Text: "the fix: the quokkabloom dial deadline is 5s now", Time: at.Add(time.Minute)},
		},
	}
	got := autoRecallSessionFor(s, at.Add(time.Hour), true, nil)
	if !strings.Contains(got, "dial deadline is 5s") {
		t.Errorf("the conclusion went with the filler line:\n%s", got)
	}
	if strings.Contains(got, "давай дальше") {
		t.Errorf("the filler line is still quoted:\n%s", got)
	}
}
