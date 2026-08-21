package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session that says the subject in twenty places must not spend twenty lines
// saying so: the hook pays for this block on every message. Removing the cap
// left the whole suite green.
//
// The cap is three. It was two until the labelled benchmark could measure what
// the third line buys: on LongMemEval blocks carrying a word few sessions use
// went from 80 to 87 of 500, and on a real store the block cost did not move,
// because a session that concludes something is paired down to two lines before
// this cap is reached.
func TestDigestShowsAtMostThreeAssistantLinesPerSession(t *testing.T) {
	now := time.Now().Add(-48 * time.Hour)
	msgs := make([]model.Message, 0, 21)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, model.Message{
			Role: "assistant",
			Text: fmt.Sprintf("kestrel retries look wrong in case %d, still kestrel", i),
			Time: now.Add(time.Duration(i) * time.Minute),
		})
	}
	s := model.Session{
		ID: "s1", Harness: "claude", Project: "p", Started: now,
		Updated: now.Add(20 * time.Minute), Messages: msgs,
	}

	block := AutoRecallDigestFor([]model.Session{s}, 4000, []string{"kestrel", "retries"})
	quoted := 0
	for _, ln := range strings.Split(block, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- User:") || strings.HasPrefix(t, "- Assistant:") {
			quoted++
		}
	}
	if quoted == 0 {
		t.Fatalf("nothing was quoted, so the cap is untested:\n%s", block)
	}
	if quoted > 3 {
		t.Errorf("session contributed %d lines; three is the cap:\n%s", quoted, block)
	}
}
