package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The last thing said before a compaction is as often a nudge as it is the
// task. Read over 96 real compactions: taking the newest user turn reported
// `working on: да`, `working on: /compact`, and once a pasted HAR file.
func TestANudgeIsNotTheTask(t *testing.T) {
	nudges := []string{
		"да", "давай дальше", "продолжай", "ok", "go on", "спасибо",
		"/compact", "/clear",
		`{"log": {"version": "1.2", "entries": [{"request": {"url": "https://example.invalid"}}]}}`,
	}
	for _, n := range nudges {
		s := model.Session{Messages: []model.Message{
			{Role: "user", Text: "cap the quokkabloom retries at four and warm the cold path"},
			{Role: "user", Text: n},
		}}
		r := ResumeFrom(s, nil)
		if !strings.Contains(r.Asked, "quokkabloom") {
			t.Errorf("%q displaced the task: %q", n, r.Asked)
		}
	}
}

// And an agent announcing an answer is not the answer.
func TestAPreambleIsNotWhatWasSettled(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "Decision: the retry budget stays at four."},
		{Role: "assistant", Text: "Хорошо, отвечаю по существу — как работает routepilot."},
	}}
	r := ResumeFrom(s, nil)
	if !strings.Contains(r.Decision, "stays at four") {
		t.Errorf("a preamble displaced the decision: %q", r.Decision)
	}
}

// A long request is still a request: the bound is there for pasted documents,
// not for someone explaining themselves.
func TestALongRequestIsStillTheTask(t *testing.T) {
	long := "the orders worker keeps exhausting its database connections under load, " +
		"and I want the first change to be the one that bounds every query with a " +
		"context so a stalled call cannot hold a connection open"
	s := model.Session{Messages: []model.Message{{Role: "user", Text: long}}}
	if r := ResumeFrom(s, nil); !strings.Contains(r.Asked, "orders worker") {
		t.Errorf("a real request was dropped as a paste: %q", r.Asked)
	}
}
