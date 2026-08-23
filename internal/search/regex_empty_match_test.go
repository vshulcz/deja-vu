package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
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
