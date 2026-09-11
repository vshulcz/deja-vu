package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The start block leads with the session that settled something, and what counts
// as settled is one predicate. A session whose newest line only announces what it
// is about to do — "Проверяю, что видео теперь играет", which fires on the state
// word inside the thing being checked — used to qualify, and then it led the
// block with a plan while the session holding the outcome waited behind it.
func TestAPlanDoesNotLeadTheStartBlock(t *testing.T) {
	now := time.Now()
	planning := model.Session{
		Harness: "claude", ID: "planning", Project: "org/app",
		Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "the exporter retries are hammering the server"},
			{Role: "assistant", Text: "Проверяю, что экспортер теперь перестал ретраить без паузы."},
		},
	}
	settled := model.Session{
		Harness: "claude", ID: "settled", Project: "org/app",
		Updated: now.Add(-12 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "what do we do about the exporter retries"},
			{Role: "assistant", Text: "we capped the retry budget at three attempts because retrying past that spread the outage"},
		},
	}

	got := BuildAutoRecall([]model.Session{planning, settled}, AutoRecallOptions{
		Mode: RecallSafe, ProjectNames: []string{"org/app"}, Now: now,
	})
	if got.Sessions == 0 {
		t.Fatal("the digest served nothing")
	}
	if len(got.IDs) == 0 || got.IDs[0] != "settled" {
		t.Errorf("the block opened on %v rather than on the session that settled something", got.IDs)
	}
	if !strings.Contains(got.Text, "capped the retry budget at three") {
		t.Errorf("the block lost the outcome:\n%s", got.Text)
	}
}
