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

// The strip must not take a person's words with it. An unclosed tag runs to
// the end of the text under the prompt hook's rule, and a person who quotes a
// tag name in a question opens one without closing it — the review of #3323
// found both, on the shapes Codex and Copilot write.
func TestContextKeepsTheQuestionUnderAnUnclosedTag(t *testing.T) {
	s := model.Session{Harness: "codex", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<environment_context>\n  <cwd>/repo</cwd>\nCan you fix the failing test in parser_test.go?"},
		{Role: "user", Text: "why does <system-reminder> show up in my prompt when I did not add it?"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "parser_test.go")
	got := b.String()
	if !strings.Contains(got, "Can you fix the failing test in parser_test.go?") {
		t.Errorf("the question under an unclosed tag was cut:\n%s", got)
	}
	if !strings.Contains(got, "show up in my prompt when I did not add it?") {
		t.Errorf("a question that names a tag lost its words:\n%s", got)
	}
}

// Two different envelopes standing apart are two blocks, not one: the words
// between them are a person's. And what a nested block leaves behind — an
// orphan closing tag, Cursor's wrapper — does not reach the agent reading the
// context (second review of #3323).
func TestContextKeepsWordsBetweenTwoEnvelopes(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "<meta>\nsome session meta\n</meta>\nI wanted to also ask: why did the deploy fail last night?\n<deja-recall>\nan old block\n</deja-recall>"},
		{Role: "user", Text: "<additional_data>\nsome file dump\n<attached_files>\nfile1.go\n</attached_files>\n</additional_data>\nplease review the retry loop off-by-one"},
		{Role: "user", Text: "<user_query>\nwhy is the retry loop dropping the last attempt\n</user_query>"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "deploy")
	got := b.String()
	if !strings.Contains(got, "why did the deploy fail last night?") {
		t.Errorf("the sentence between two envelopes was eaten:\n%s", got)
	}
	if strings.Contains(got, "</additional_data>") || strings.Contains(got, "<user_query>") {
		t.Errorf("a bare tag reached the context:\n%s", got)
	}
	if !strings.Contains(got, "why is the retry loop dropping the last attempt") {
		t.Errorf("the words inside Cursor's wrapper are gone:\n%s", got)
	}
}

// Naming a tag is not writing one: a sentence that quotes Cursor's wrapper
// keeps its words, where a global strip of the bare tag gutted it (third
// review of #3323).
func TestContextKeepsASentenceThatNamesTheWrapper(t *testing.T) {
	s := model.Session{Harness: "claude", Project: "p", ID: "id", Messages: []model.Message{
		{Role: "user", Text: "the three things Cursor wraps around a user message: `<user_query>`, `<additional_data>`, `<attached_files>`"},
	}}
	var b bytes.Buffer
	PrintContext(&b, s, "cursor wraps")
	if !strings.Contains(b.String(), "`<user_query>`") {
		t.Errorf("the sentence lost the tag it was naming:\n%s", b.String())
	}
}
