package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The line a recall block opens with is the first thing an agent reads, and on
// a long session it was taken from the top of the transcript rather than from
// the part that matched. Measured on a real store: for a question about a car's
// CAN bus, the block led with "продолжай дальше" while the matching line sat
// three lines below — the recall was right and looked worthless.
func TestRecallBlockDoesNotOpenWithFillerFromTheTop(t *testing.T) {
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	msgs := []model.Message{
		{Role: "user", Text: "carry on then", Time: start},
		{Role: "assistant", Text: "opened the file and looked around", Time: start.Add(time.Minute)},
	}
	for i := 0; i < 200; i++ {
		at := start.Add(time.Duration(i+2) * time.Minute)
		msgs = append(msgs,
			model.Message{Role: "user", Text: "keep going", Time: at},
			model.Message{Role: "assistant", Text: "adjusted it and ran the suite again", Time: at.Add(time.Second)},
		)
	}
	// The answer, deep in the session, and no user turn naming the subject —
	// which is the case the fallback used to handle by grabbing the top.
	msgs = append(msgs, model.Message{
		Role: "assistant",
		Text: "the kestrel handshake had to be retried twice before the socket settled",
		Time: start.Add(400 * time.Minute),
	})

	s := model.Session{
		ID: "long-1", Harness: "claude", Project: "proj",
		Started: start, Updated: start.Add(400 * time.Minute), Messages: msgs,
	}
	got := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"kestrel", "handshake"})
	if got == "" {
		t.Fatal("nothing was recalled, so this test cannot see what it is about")
	}
	if strings.Contains(got, "carry on then") {
		t.Errorf("the block opens with the first line of the session instead of the part that matched:\n%s", got)
	}
	if !strings.Contains(got, "kestrel") {
		t.Errorf("the block does not show the line that matched:\n%s", got)
	}
}
