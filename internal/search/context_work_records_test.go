package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The context digest is the conversation, not the machinery under it. What an
// agent did — the files it touched, the spans it replaced, the commands it ran
// — is indexed and searchable by role, and before those roles were labelled
// honestly (#560) they arrived as `user` and filled this with blocks nobody
// meant by context.
//
// One exception, measured: a work record that carries the query is evidence,
// not machinery. Four in five of everything a query matches lives in these
// records, and holding all of them back left the digest with nothing about the
// subject on 1 of 12 real questions. They come in only when matched, never as
// filler, and capped so a log cannot take the budget the conversation needs.
func TestContextDigestLeavesTheMachineryOut(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "work", ID: "w1",
		Messages: []model.Message{
			{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once"},
			{Role: roleToolOutput, Text: "npm ERR! code ELIFECYCLE\nnpm ERR! errno 1"},
			{Role: "command", Text: "$ npm test"},
			{Role: "files", Text: "/w/app/retry.go\n/w/app/queue.go"},
			{Role: "edit", Text: "/w/app/retry.go\nthe body that was replaced"},
			{Role: "assistant", Text: "We spread the wakeups over a second, bounded by the poll interval."},
		},
	}
	var b strings.Builder
	PrintContext(&b, s, "retry")
	out := b.String()

	// The conversation is there, or the assertions below pass on an empty
	// digest.
	if !strings.Contains(out, "the retry queue stalls on staging") {
		t.Fatalf("wrong fixture, the conversation is missing:\n%s", out)
	}
	if !strings.Contains(out, "We spread the wakeups over a second") {
		t.Fatalf("wrong fixture, the answer is missing:\n%s", out)
	}

	// Nothing that fails to say the query gets in, whatever its role.
	for _, body := range []string{"npm ERR!", "$ npm test"} {
		if strings.Contains(out, body) {
			t.Errorf("an unmatched work record reached the digest: %q\n%s", body, out)
		}
	}
	// The record that does say it is the answer to "where does retry live".
	if !strings.Contains(out, "/w/app/retry.go") {
		t.Errorf("the matched work record was held back:\n%s", out)
	}
}
