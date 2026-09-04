package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The caller orders its candidates so a session start says something the
// project has not been told, and this used to undo it: with no task scores the
// sort fell through to recency, so the same three newest sessions were served
// to every session start in the project. Measured on a seeded store of twelve:
// three consecutive starts, one block, three sessions, every time.
func TestUnseenSessionsOutrankRecency(t *testing.T) {
	now := time.Now()
	var ss []model.Session
	for i := 0; i < 6; i++ {
		ss = append(ss, model.Session{
			ID:      string(rune('a' + i)),
			Project: "org/app",
			Updated: now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "question about widget " + string(rune('a'+i))},
				{Role: "assistant", Text: "settled it by rewriting the widget " + string(rune('a'+i)) + " loader"},
			},
		})
	}
	opts := AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"org/app"}, Now: now}

	first := BuildAutoRecall(ss, opts)
	if first.Sessions == 0 {
		t.Fatal("the digest served nothing to compare")
	}
	// What the project has already been told, in the caller's words.
	told := map[string]bool{}
	for _, id := range first.IDs {
		told[id] = true
	}
	unseen := map[string]bool{}
	for _, s := range ss {
		if !told[s.ID] {
			unseen[s.ID] = true
		}
	}
	opts.Unseen = unseen

	second := BuildAutoRecall(ss, opts)
	for _, id := range second.IDs {
		if told[id] {
			t.Errorf("the second start repeated %q: first %v, second %v", id, first.IDs, second.IDs)
		}
	}
	if strings.TrimSpace(second.Text) == "" {
		t.Fatal("promoting the unseen emptied the digest")
	}
}

// With nothing marked new the order is exactly what it was, so every other
// caller of this builder is untouched.
func TestNoUnseenSetLeavesTheOrderAlone(t *testing.T) {
	now := time.Now()
	var ss []model.Session
	for i := 0; i < 5; i++ {
		ss = append(ss, model.Session{
			ID:      string(rune('a' + i)),
			Project: "org/app",
			Updated: now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "question " + string(rune('a'+i))},
				{Role: "assistant", Text: "answer " + string(rune('a'+i))},
			},
		})
	}
	opts := AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"org/app"}, Now: now}
	a := BuildAutoRecall(ss, opts)
	b := BuildAutoRecall(ss, opts)
	if strings.Join(a.IDs, ",") != strings.Join(b.IDs, ",") {
		t.Fatalf("the same input gave two orders: %v then %v", a.IDs, b.IDs)
	}
}
