package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session where the agent only ever said it could not proceed carries nothing
// to reuse, and recall drops it. The wording that says so has to come from the
// agent's own turns: tool output is full of "permission denied", and a session
// that hit that error and then explained it is exactly the memory worth keeping.
func TestRefusalInToolOutputDoesNotDropTheSession(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "why does the deploy fail on the staging cluster?"},
			{Role: "tool-output", Text: "deploy: permission denied opening the deploy kubeconfig for deploy"},
			{Role: "tool-output", Text: "deploy: permission denied again on the deploy retry, deploy aborted"},
			{Role: "assistant", Text: "the deploy failed because the kubeconfig was mounted read-only; we mounted it read-write and it went through"},
		},
	}
	got := AutoRecallDigestFor([]model.Session{s}, 4000, []string{"deploy"})
	if !strings.Contains(got, "mounted it read-write") {
		t.Errorf("session dropped as blocked over wording in its tool output; digest was %q", got)
	}
}

// The other side: the same wording from the agent itself, with nothing else
// said, is a session that reached no conclusion, and recall must not spend an
// agent's opening context on it.
func TestRefusalFromTheAgentDropsTheSession(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "why does the deploy fail on the staging cluster?"},
			{Role: "assistant", Text: "permission denied, I cannot read that file"},
		},
	}
	if got := AutoRecallDigestFor([]model.Session{s}, 4000, []string{"deploy"}); strings.Contains(got, "permission denied") {
		t.Errorf("a session the agent only ever refused was recalled: %q", got)
	}
}
