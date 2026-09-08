package search

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Every user turn was kept whatever it held, so a session whose transcript
// carries the harness talking to itself — Claude Code's teammate messages,
// task notifications, idle-notification JSON — spent the context budget on
// that and never reached the turns about the question. On this machine's
// index, ten real questions came back 38% envelope, three of them 89% (#3323).
func TestContextSkipsTurnsThatAreOnlyTheHostTalking(t *testing.T) {
	long := strings.Repeat("Review of the damaged-index JSON fix, point four. ", 120)
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "Another Claude session sent a message:\n<teammate-message teammate_id=\"rev354\">\n" + long + "\n</teammate-message>"},
		{Role: "user", Text: "<teammate-message teammate_id=\"rev354\">\n{\"type\":\"idle_notification\",\"from\":\"rev354\"}\n</teammate-message>"},
		{Role: "user", Text: "follow HERMES_HOME, the variable Hermes itself reads"},
		{Role: "assistant", Text: "the hermes reader now takes HERMES_HOME before the profiles root"},
	}}

	var b bytes.Buffer
	PrintContext(&b, s, "HERMES_HOME")
	got := b.String()
	if strings.Contains(got, "idle_notification") {
		t.Errorf("an idle notification is in the context handed to another agent:\n%s", got)
	}
	if strings.Contains(got, "teammate_id") {
		t.Errorf("a teammate envelope is in the context handed to another agent:\n%s", got)
	}
	if !strings.Contains(got, "HERMES_HOME") {
		t.Errorf("the turns about the question never made it:\n%s", got)
	}
}

// A turn that is the person's words with an envelope appended is the person's
// turn and stays whole — the words are what the reader asked about.
func TestContextKeepsATurnThatHasWordsBesideTheEnvelope(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "why does the retry loop drop the last attempt\n<system-reminder>the file was edited</system-reminder>"},
		{Role: "assistant", Text: "because the counter is compared with < instead of <="},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "retry loop")
	if !strings.Contains(b.String(), "why does the retry loop drop the last attempt") {
		t.Errorf("the person's words went with the envelope:\n%s", b.String())
	}
}

// The fallback overview, printed when no turn carries the whole query, is
// context too: it must not be an agent's idle notifications either.
func TestContextOverviewSkipsTheHostsOwnTurns(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<task-notification>agent qa-zed2 finished</task-notification>"},
		{Role: "user", Text: "index the copied Zed store and compare the counts"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "a query that matches nothing at all here")
	if strings.Contains(b.String(), "task-notification") {
		t.Errorf("the overview is the host's own turn:\n%s", b.String())
	}
}
