package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An agent run from inside a session writes its stdout into that session's
// transcript, tool-call lines included, and those lines carry the queries it
// sent. Asked which wording option had been chosen, recall led with the log of
// that very question being asked and the agent answered with an invented
// phrase (#2067).
func TestTheLogOfACallDoesNotAnswerTheQuestionItAsked(t *testing.T) {
	now := time.Now()
	// A real working session that happens to contain the log.
	logged := model.Session{
		ID: "logged", Harness: "claude", Project: "deja-vu",
		Path: "/Users/x/.claude/projects/deja-vu/logged.jsonl", Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "assistant", Text: "> builder · gpt-5.6-luna\n" +
				`⚙ deja_recall {"query":"repository description wording","limit":10}` + "\n" +
				"ran the sweep and moved on", Time: now},
		},
	}
	answered := model.Session{
		ID: "answered", Harness: "claude", Project: "deja-vu",
		Path: "/Users/x/.claude/projects/deja-vu/answered.jsonl", Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "which repository description wording did we pick", Time: now},
			{Role: "assistant", Text: "You picked the task-first option, so it now opens with Search your past AI coding sessions.", Time: now},
		},
	}

	hits, err := Run([]model.Session{logged, answered}, Options{Query: "repository description wording", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("nothing matched at all")
	}
	if hits[0].Session.ID != "answered" {
		t.Errorf("the log of the call outranked the answer: %s", hits[0].Session.ID)
	}
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "deja_recall {") {
				t.Errorf("a call log was quoted back as a snippet: %q", sn)
			}
		}
	}
}

// The fact that a call happened stays in the transcript — `deja how` and
// `deja fix` are built on exactly that — so only matching is affected.
func TestTheCallItselfIsStillInTheTranscript(t *testing.T) {
	line := `⚙ deja_how {"what":"go test"}`
	s := model.Session{
		ID: "kept", Harness: "claude", Project: "p",
		Messages: []model.Message{{Role: "assistant", Text: line}},
	}
	if got := s.Messages[0].Text; got != line {
		t.Fatalf("the transcript was rewritten: %q", got)
	}
	if withoutOwnCallLog(line) != "" {
		t.Errorf("the line was not removed from matching: %q", withoutOwnCallLog(line))
	}
}

// Prose about the tools is ordinary text and has to stay searchable, or a
// session explaining how recall works becomes unfindable.
func TestProseAboutTheToolsIsKept(t *testing.T) {
	for _, text := range []string{
		"recall_context returns a digest of the best matching session",
		"we should call deja_recall before debugging anything",
		"deja_fix takes the failing output verbatim",
	} {
		if got := withoutOwnCallLog(text); got != text {
			t.Errorf("prose was treated as a call log: %q -> %q", text, got)
		}
	}
}

// Only the log lines go; the rest of the message is still matched.
func TestOnlyTheCallLinesAreRemoved(t *testing.T) {
	text := "looked at the release\n" +
		`⚙ deja_recall {"query":"release discussion permission"}` + "\n" +
		"the missing permission was discussions: write"
	got := withoutOwnCallLog(text)
	if strings.Contains(got, "deja_recall {") {
		t.Errorf("the call line survived: %q", got)
	}
	for _, want := range []string{"looked at the release", "discussions: write"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was removed with it", want)
		}
	}
}
