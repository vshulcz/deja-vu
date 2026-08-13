package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func recallOf(t *testing.T, msgs ...model.Message) string {
	t.Helper()
	s := model.Session{
		Harness: "claude", ID: "s", Project: "goprojects/deja-vu",
		Updated: time.Now(), Messages: msgs,
	}
	return autoRecallSession(s, time.Now(), true)
}

// A session that never got past a permission prompt has nothing to reuse. One
// had been taking a slot in every agent's opening context on a real store for
// twenty days.
func TestBlockedSessionIsNotRecalled(t *testing.T) {
	got := recallOf(t,
		model.Message{Role: "user", Text: "search the history for the openclaw harness test"},
		model.Message{Role: "tool-output", Text: "Claude requested permissions to use mcp__deja__recall, but you haven't granted it yet."},
		model.Message{Role: "assistant", Text: "Permission for `mcp__deja__recall` not granted — call blocked. Approve tool, then me search again."},
	)
	if got != "" {
		t.Fatalf("a session that only reported being blocked reached an agent:\n%s", got)
	}
}

// Work that hit a wall and then got past it is the opposite: that is exactly
// the memory worth having.
func TestBlockedThenRecoveredIsRecalled(t *testing.T) {
	got := recallOf(t,
		model.Message{Role: "user", Text: "index the store and search it"},
		model.Message{Role: "assistant", Text: "Permission for the tool was not granted — call blocked."},
		model.Message{Role: "assistant", Text: "Approved now; the index rebuilt in 2.1s and the query returns the session."},
	)
	if got == "" {
		t.Fatal("a session that recovered from a block was dropped")
	}
}

// And a session merely discussing permissions is ordinary work.
func TestTalkingAboutPermissionsIsRecalled(t *testing.T) {
	got := recallOf(t,
		model.Message{Role: "user", Text: "why does the hook need permission to read the store?"},
		model.Message{Role: "assistant", Text: "It does not; the reader opens the index directly and only the MCP path asks."},
	)
	if got == "" {
		t.Fatal("a discussion about permissions was mistaken for a denied one")
	}
}
