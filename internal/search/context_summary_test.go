package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// summarySession is a session whose window holds a compaction summary — the
// longest message a session ever has, naming everything it did — beside the
// turns that actually discussed the subject.
func summarySession() model.Session {
	summary := "Summary:\n1. **Primary Request and Intent:**\n\n" +
		"   The standing instruction is to keep improving the tool.\n" +
		"2. **Key Technical Concepts:**\n   - the retry budget, the scheduler, the failover\n" +
		strings.Repeat("   - a line about something else entirely\n", 120)
	now := time.Now()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: summary, Time: now},
			{Role: "user", Text: "what did we settle about the retry budget?", Time: now.Add(time.Minute)},
			{Role: "assistant", Text: "We capped the retry budget at three attempts and let the fourth fail loudly.", Time: now.Add(2 * time.Minute)},
		},
	}
}

// A compaction summary names everything the session did, so it matches almost
// any question and then fills the window on its own. Measured over eight
// questions an agent would ask, on a real store: three answers came back
// carrying one, at 95% of the answer each — 44% of every byte recall_context
// served, none of it an answer.
func TestAContextWindowIsNotFilledByACompactionSummary(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, summarySession(), "retry budget")
	got := b.String()

	if !strings.Contains(got, "capped the retry budget at three") {
		t.Errorf("the turn that answers is missing:\n%s", got)
	}
	if strings.Contains(got, "a line about something else entirely") {
		t.Errorf("the summary was printed whole (%d bytes):\n%s", len(got), got[:min(len(got), 400)])
	}
	if !strings.Contains(got, "compaction summary") {
		t.Errorf("nothing says the summary was cut:\n%s", got)
	}
	// The lines of it that do carry the query are what it contributes.
	if !strings.Contains(got, "the retry budget, the scheduler, the failover") {
		t.Errorf("the matching line of the summary is missing:\n%s", got)
	}
}

// Asked nothing — `deja ctx <id>`, the MCP resource read — there is nothing to
// match, and the summary is the best account of the session there is.
func TestWithNoQueryTheSummaryIsKept(t *testing.T) {
	var b bytes.Buffer
	PrintContext(&b, summarySession(), "")
	if !strings.Contains(b.String(), "a line about something else entirely") {
		t.Error("a session read with no query lost its summary")
	}
}

// A turn that merely opens with the word is a person writing, not a harness.
func TestAnOrdinaryTurnCalledSummaryIsUntouched(t *testing.T) {
	now := time.Now()
	s := model.Session{
		Harness: "claude", ID: "s2", Project: "app", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "Summary: the retry budget is three and the scheduler is single-writer, which is what we agreed.", Time: now},
		},
	}
	var b bytes.Buffer
	PrintContext(&b, s, "retry budget")
	if got := b.String(); !strings.Contains(got, "single-writer") {
		t.Errorf("an ordinary turn was clipped as a compaction summary:\n%s", got)
	}
}
