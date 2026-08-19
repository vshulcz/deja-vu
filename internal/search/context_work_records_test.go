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
// Measured by removing the filter: with isWorkRecord returning false, the
// digest grew "## files" and "## edit" blocks and no test in this package
// failed. This is that test.
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

	for _, marker := range []string{"## files", "## edit", "## tool-output", "## command"} {
		if strings.Contains(out, marker) {
			t.Errorf("the digest carries a %s block:\n%s", marker, out)
		}
	}
	// And not just the headings: the bodies must not arrive under another
	// role either. These are what actually bite — a stubbed isWorkRecord
	// leaks the files and edit blocks, while tool output and commands are
	// held back by the query filter above as well, so their absence here is
	// weaker evidence than it looks.
	for _, body := range []string{"/w/app/queue.go", "the body that was replaced", "npm ERR!", "$ npm test"} {
		if strings.Contains(out, body) {
			t.Errorf("the digest carries %q:\n%s", body, out)
		}
	}
}
