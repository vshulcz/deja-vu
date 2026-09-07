package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A person who pastes a hook's status bar and asks about it gets the
// question as the title, not the bar (#3168).
func TestMatchedUserLineSkipsThePastedStatusBar(t *testing.T) {
	s := model.Session{Messages: []model.Message{{
		Role: "user",
		Text: "UserPromptSubmit says: deja-vu — you have been here: \"Summary: 1. Primary Request…\" (Aug 11 · via: task-notification)\nwhy does the prompt hook quote a task notification as a title?",
	}}}
	got := MatchedUserLine(s, []string{"prompt", "hook", "notification"})
	if got != "why does the prompt hook quote a task notification as a title?" {
		t.Fatalf("title = %q", got)
	}
}
