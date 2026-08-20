package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session answers the question in one line and echoes an ordinary word of it
// in another. The block has to open on the line that answers.
//
// The question slot took the best-matching user line whatever it carried, and
// the block is printed with that slot first, so a session saying "decide" three
// times about nothing led with it. Measured on a real store: "what did we decide
// about mm_status" opened on "1. **Decide**: User settings (global) or project
// settings?" while the answer sat below.
func TestBlockOpensOnTheLineThatAnswers(t *testing.T) {
	at := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	s := model.Session{
		ID: "one", Harness: "claude", Project: "app", Started: at, Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "decide whether to decide this now or decide it later", Time: at},
			{Role: "assistant", Text: "we decided quicksilver retries are capped at four", Time: at.Add(time.Minute)},
		},
	}
	// Terms arrive most-identifying first, the way the hook orders them.
	block := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"quicksilver", "decide"})
	first := ""
	for _, ln := range strings.Split(block, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "- User:") || strings.HasPrefix(ln, "- Assistant:") {
			first = ln
			break
		}
	}
	if first == "" {
		t.Fatalf("no quoted line in the block:\n%s", block)
	}
	if !strings.Contains(first, "quicksilver") {
		t.Fatalf("the block opens on a line that only echoes a word of the question:\n%s", first)
	}
}
