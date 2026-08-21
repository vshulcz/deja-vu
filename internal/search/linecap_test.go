package search

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A quoted line was cut at 220 characters, and what it cut was the tail — where
// the path, the number or the name sits. Replayed against what the agent
// actually answered next on a real store, blocks carrying words that answer
// went on to use rose from 25 to 26 of 100, blocks carrying a conclusion from
// 57 to 60, for 11 tokens a message. Cutting at 440 instead loses ground again
// (24 of 100): the longer a line, the fewer sessions fit beside it.
func TestQuotedLineKeepsItsTail(t *testing.T) {
	line := "про pgbouncer мы разбирались долго, " +
		strings.Repeat("перебрали разные варианты и померили каждый, ", 4) +
		"и держим 40 коннектов на шард"
	s := model.Session{
		Harness: "claude", ID: "tail", Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer"},
			{Role: "assistant", Text: line},
		},
	}
	got := AutoRecallDigestForAsked([]model.Session{s}, 4096,
		[]string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "40 коннектов на шард") {
		t.Errorf("the line was cut before the number that answers it:\n%s", got)
	}
}
