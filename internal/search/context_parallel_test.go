package search

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// mixedContextSession is the shape that broke rendering only the window in
// #1742: prose next to tool dumps and numbered output, so collapsing changes
// sizes, plus a fenced block and an escape byte that the renderers treat
// differently from ordinary text.
func mixedContextSession(turns int) model.Session {
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "claude", Project: "proj", ID: "mixed", Updated: base}
	for i := range turns {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		var text string
		switch i % 5 {
		case 0:
			text = fmt.Sprintf("turn %d: the resharding question came back today", i)
		case 1:
			text = fmt.Sprintf("  %d | 0x%04x  some tool dump line\n  %d | 0x%04x  another\nwe decided to cap retries at 3", i, i, i+1, i+1)
		case 2:
			text = fmt.Sprintf("```go\nfunc turn%d() {}\n```", i)
		case 3:
			text = strings.Repeat(fmt.Sprintf("turn %d prose that runs long enough to matter. ", i), 40)
		case 4:
			text = fmt.Sprintf("turn %d\x1b[31m coloured \x1b[0m and \t tabbed\n\n\n  spaced   out  ", i)
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: base.Add(time.Duration(i) * time.Minute)})
	}
	return s
}

// The digest is the same bytes however many cores render it: turns are written
// back by index, so parallel rendering cannot reorder or drop one (#1790).
func TestContextIsTheSameBytesOnOneCoreAndMany(t *testing.T) {
	s := mixedContextSession(200)
	for _, query := range []string{"", "resharding", "retries", "nothing matches this"} {
		var one, many bytes.Buffer
		withRenderWorkers(t, 1, func() { PrintContext(&one, s, query) })
		withRenderWorkers(t, 8, func() { PrintContext(&many, s, query) })
		if one.String() != many.String() {
			t.Errorf("query %q rendered differently on 8 cores than on 1:\n--- one ---\n%s\n--- many ---\n%s", query, one.String(), many.String())
		}
		if one.Len() == 0 {
			t.Errorf("query %q rendered nothing, so this proves nothing", query)
		}
	}
}

// Below the threshold the turns render in place; the output still has to match
// what the pool produces.
func TestShortSessionsRenderInPlaceAndMatch(t *testing.T) {
	s := mixedContextSession(6)
	var small, pooled bytes.Buffer
	withRenderWorkers(t, 8, func() { PrintContext(&small, s, "resharding") })
	withRenderWorkers(t, 1, func() { PrintContext(&pooled, s, "resharding") })
	if small.String() != pooled.String() {
		t.Errorf("a six-turn session rendered differently:\n%s\n---\n%s", small.String(), pooled.String())
	}
	if !strings.Contains(small.String(), "resharding") {
		t.Errorf("the matched turn is missing:\n%s", small.String())
	}
}

func withRenderWorkers(t *testing.T, n int, f func()) {
	t.Helper()
	prev := renderContextTurnsWorkers
	renderContextTurnsWorkers = func() int { return n }
	defer func() { renderContextTurnsWorkers = prev }()
	f()
}
