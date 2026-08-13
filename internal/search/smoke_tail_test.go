package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A harness check states the task first and how to answer last. Matching only
// the prompt's opening let those through: on a real store, two of the three
// sessions handed to an agent at session start were this exact shape.
func TestSmokeTestAskCanBeTheLastSentence(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "Use the deja recall tool to search for: openclaw deja harness live test alpha. Reply with the harness name of the top match only."},
			{Role: "assistant", Text: "openclaw"},
		},
	}
	if got := autoRecallSession(s, time.Now(), true); got != "" {
		t.Fatalf("smoke test reached an agent's context:\n%s", got)
	}
}

// And work that merely talks about replying must survive, however short the
// answer is.
func TestProseAboutRepliesIsNotASmokeTest(t *testing.T) {
	for _, text := range []string{
		"the gateway should reply with 404 when the token is stale, right?",
		"we answer with the cached value if the upstream is down",
	} {
		s := model.Session{
			Harness: "claude", ID: "work", Project: "goprojects/deja-vu",
			Updated: time.Now(),
			Messages: []model.Message{
				{Role: "user", Text: text},
				{Role: "assistant", Text: "yes, fixed"},
			},
		}
		if got := autoRecallSession(s, time.Now(), true); got == "" {
			t.Fatalf("real work was dropped as a smoke test: %q", text)
		}
	}
}

// The narrow case the filter was written for still has to go.
func TestClassicSmokeTestStillDropped(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke2", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "Reply with the single word OK"},
			{Role: "assistant", Text: "OK"},
		},
	}
	if got := autoRecallSession(s, time.Now(), true); !strings.EqualFold(got, "") {
		t.Fatalf("classic smoke test survived:\n%s", got)
	}
}
