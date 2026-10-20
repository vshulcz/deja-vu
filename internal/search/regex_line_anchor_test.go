package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ^ and $ in a --re pattern anchor to a line, as they do in grep. Go's
// default anchors the whole text, so a tool output or a long reply of many
// lines only ever matched on its first or last line: `^panic:` missed a
// panic that came after any other output.
func TestRegexAnchorsMatchAtLineBreaks(t *testing.T) {
	tool := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "tool", Text: "ok  example.com/a  0.2s\npanic: runtime error: index out of range\ngoroutine 1 [running]:\nexit status 2"},
		},
	}
	for _, q := range []string{"^panic: runtime", "out of range$", "^goroutine 1"} {
		hits, err := Run([]model.Session{tool}, Options{Query: q, Regex: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("--re %q: %d hits, want the session whose output has that line", q, len(hits))
		}
	}
	// Control: an anchor still anchors. The text is on a line, not at its start.
	hits, err := Run([]model.Session{tool}, Options{Query: "^runtime error", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("--re ^runtime error matched mid-line: %d hits", len(hits))
	}

	// The excerpt joins a message's lines, and still centres on the line the
	// anchored pattern matched.
	reply := model.Session{
		Harness: "claude", Project: "p", ID: "s2",
		Messages: []model.Message{
			{Role: "assistant", Text: strings.Repeat("Read through the pool code and the retry wrapper around it. ", 12) +
				"\nThe flaky test was TestPoolDrain, fixed by raising the drain timeout"},
		},
	}
	for _, q := range []string{"^The flaky test", "drain timeout$"} {
		hits, err := Run([]model.Session{reply}, Options{Query: q, Regex: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("--re %q: %d hits, want 1", q, len(hits))
			continue
		}
		if !strings.Contains(strings.Join(hits[0].Snippets, " "), "TestPoolDrain") {
			t.Errorf("--re %q: excerpt %q is not the line the pattern matched", q, hits[0].Snippets)
		}
	}
}
