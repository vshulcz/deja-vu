package search

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
)

// `x*` matches the empty string, so it hits between every pair of characters.
// Counting those made a session with no `x` in it report 88 matches and rank
// above nothing, which is the opposite of what the reader asked for.
func TestEmptyRegexMatchIsNotAMatch(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "the retry loop keeps firing"},
			{Role: "user", Text: "we set retry-backoff to 5s and it held"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "x*", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("`x*` found %d sessions with %d matches; text holds no x, so it holds no match",
			len(hits), hits[0].Count)
	}
	// The control: the same session, a pattern that really is there.
	hits, err = Run([]model.Session{s}, Options{Query: "retry", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Count != 2 {
		t.Fatalf("control: `retry` found %d sessions, want 1 with 2 matches", len(hits))
	}
}

// A zero-width hit wrapped in colour paints an escape pair around nothing, once
// per character: 977 bytes for a 44-column line.
func TestHighlightLeavesZeroWidthMatchesAlone(t *testing.T) {
	const line = "we set retry-backoff to 5s and it held"
	if got := highlight(line, "x*", true, true); got != line {
		t.Errorf("highlight painted %d bytes over a %d-byte line for a zero-width pattern:\n%q",
			len(got), len(line), got)
	}
	// The control: a pattern with width still gets highlighted.
	if got := highlight(line, "retry", true, true); !strings.Contains(got, cMatch+"retry"+cReset) {
		t.Errorf("control: a real match is no longer highlighted: %q", got)
	}
}

// An invalid --re pattern must report the pattern the user typed, not deja's
// case-insensitive prefix. Compiling "(?i)"+query echoed `(?i)(` back at someone
// who wrote `(`, which reads like their own text and hides the real mistake.
func TestRegexErrorNamesTheUserPattern(t *testing.T) {
	_, err := Run([]model.Session{}, Options{Query: "(", Regex: true})
	if err == nil {
		t.Fatal("an invalid regex must be reported, not swallowed")
	}
	if strings.Contains(err.Error(), "(?i)") {
		t.Fatalf("the error leaks deja's injected (?i) prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "`(`") && !strings.Contains(err.Error(), ": (") {
		t.Fatalf("the error does not name the user's pattern: %v", err)
	}
}

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
