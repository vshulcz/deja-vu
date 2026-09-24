package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func ranStore(t *testing.T, msgs ...model.Message) (string, model.Session) {
	t.Helper()
	hermeticEnv(t)
	dir := t.TempDir()
	s := model.Session{Harness: "claude", ID: "ran1", Messages: msgs}
	index.BuildSessionFactsForTest(dir, []model.Session{s})
	return dir, s
}

// The line is the only part of a recall answer that is not a claim, so it says
// the outcome when the transcript recorded one — and says nothing about an
// outcome nobody recorded.
func TestTheRanLineSaysWhatTheTranscriptRecorded(t *testing.T) {
	dir, s := ranStore(t, model.Message{Role: "command", Text: "$ cd /Users/x/svc; SVC_FIXTURES=./fixtures make test  → exit 0"})
	got := recallRanLine(dir, s)
	if !strings.Contains(got, "SVC_FIXTURES=./fixtures make test") {
		t.Errorf("the command is not on the line: %q", got)
	}
	if strings.Contains(got, "cd /Users/x/svc") || strings.Contains(got, "$ ") {
		t.Errorf("the line carries one machine's way of getting there: %q", got)
	}
	if !strings.Contains(got, "exit 0 here") {
		t.Errorf("a command the transcript saw pass does not say so: %q", got)
	}

	dir, s = ranStore(t, model.Message{Role: "command", Text: "$ go build ./..."})
	if got := recallRanLine(dir, s); !strings.Contains(got, "go build ./...") || strings.Contains(got, "exit") {
		t.Errorf("a command with no recorded outcome reads as %q", got)
	}
}

// A session that ran several things is worth one line, and the one worth having
// is the command that worked — not the newest, which on a session that ended
// badly is the failure.
func TestTheRanLinePrefersTheCommandThatPassed(t *testing.T) {
	dir, s := ranStore(t,
		model.Message{Role: "command", Text: "$ make test  → exit 0"},
		model.Message{Role: "command", Text: "$ make test-broken  → exit 2"},
	)
	got := recallRanLine(dir, s)
	if !strings.Contains(got, "make test ") && !strings.HasSuffix(strings.SplitN(got, " —", 2)[0], "make test") {
		t.Errorf("the passing command is not the one shown: %q", got)
	}
	if strings.Contains(got, "exit 2") {
		t.Errorf("the failing command was chosen: %q", got)
	}
	if strings.Count(got, "\n") > 0 {
		t.Errorf("the line is more than one line: %q", got)
	}
}

// An index built before the table existed, and a session that ran nothing, both
// say nothing rather than half a line.
func TestTheRanLineIsEmptyWithoutEvidence(t *testing.T) {
	hermeticEnv(t)
	if got := recallRanLine(t.TempDir(), model.Session{Harness: "claude", ID: "nobody"}); got != "" {
		t.Errorf("an index with no table produced %q", got)
	}
	dir, s := ranStore(t, model.Message{Role: "user", Text: "what did we decide"})
	if got := recallRanLine(dir, s); got != "" {
		t.Errorf("a session that ran nothing produced %q", got)
	}
}
