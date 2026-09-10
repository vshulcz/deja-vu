package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A note is read back much later, by which point only a decision still means
// anything. Read from a real store, what promote would have kept was the first
// user turn and the last assistant turn — so notes held "asked: прочитай <file>
// целиком и начинай" and, on a session that had been compacted, "asked: Summary:
// 1. **Primary Request and Intent:** …".
func TestTheNoteKeepsTheDecisionNotTheLastThingInHand(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "Summary:\n1. **Primary Request and Intent:**\n   the quokkabloom fetcher", Time: now},
			{Role: "user", Text: "why does the quokkabloom fetcher time out on a cold index?", Time: now.Add(time.Minute)},
			{Role: "assistant", Text: "Decision: the quokkabloom retry budget stays at four, and the cold path warms the index first.", Time: now.Add(2 * time.Minute)},
			// Where the session stopped: a line that is neither a tool echo nor a
			// decision, so only preferring the decision can reach the one above.
			{Role: "assistant", Text: "ran the suite again and the cold path is still slow here", Time: now.Add(3 * time.Minute)},
		},
	}
	note := distillSession(s)
	if strings.Contains(note, "Primary Request and Intent") {
		t.Errorf("a compaction summary is kept as what was asked:\n  %s", note)
	}
	if !strings.Contains(note, "why does the quokkabloom fetcher time out") {
		t.Errorf("the question a person asked is missing:\n  %s", note)
	}
	if !strings.Contains(note, "retry budget stays at four") {
		t.Errorf("the decision is missing:\n  %s", note)
	}
	if strings.Contains(note, "still slow here") {
		t.Errorf("the note kept where the session stopped instead of what it decided:\n  %s", note)
	}
}

// A session that reached no decision still keeps what it ended on: something is
// better than a note that says only what was asked.
func TestASessionWithNoDecisionStillKeepsItsEnding(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: "user", Text: "look at the quokkabloom fetcher", Time: now},
			{Role: "assistant", Text: "read through it, nothing stood out yet", Time: now.Add(time.Minute)},
		},
	}
	note := distillSession(s)
	if !strings.Contains(note, "nothing stood out") {
		t.Errorf("the ending is missing from a session that decided nothing:\n  %s", note)
	}
}
