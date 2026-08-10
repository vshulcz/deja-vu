package main

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// earlierAttemptWarning joins its lines into the agent-facing injection block.
// The session id it names is free text from the transcript, so a crafted
// sessionId with an escape or an invisible run must not ride in unaltered — the
// same treatment the rejected-warning already gives it.
func TestEarlierAttemptWarningSanitisesTheID(t *testing.T) {
	now := time.Now()
	msgs := []model.Message{{Role: "user", Text: "database connection pool timeout rotation retry attempt", Time: now}}
	older := model.Session{
		ID: "att\u202empt\u200bid", Harness: "claude", Project: "p",
		Updated: now.Add(-48 * time.Hour), Messages: msgs,
	}
	newer := model.Session{
		ID: "newer", Harness: "claude", Project: "p",
		Updated: now.Add(-1 * time.Hour), Messages: msgs,
	}
	got := earlierAttemptWarning([]model.Session{older, newer})
	if got == "" {
		t.Fatal("expected an earlier-attempt warning; the fixture did not trigger one")
	}
	for _, r := range got {
		if r != '\n' && unicode.IsControl(r) {
			t.Fatalf("warning carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("warning carried %U: %q", bad, got)
		}
	}
}
