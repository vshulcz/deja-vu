package main

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The packet read an exit marker out of the command text, which codex and
// opencode append and Claude does not — so on a Claude transcript where the suite
// failed and was then fixed, both runs arrived as "[recorded]" and a resuming
// agent could not tell whether the work was green when it was interrupted.
func TestThePacketSaysWhetherTheCommandPassed(t *testing.T) {
	now := time.Now().UTC()
	at := func(i int) time.Time { return now.Add(time.Duration(i) * time.Minute) }
	session := model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go test ./internal/exporter/ -run TestBackoff -count=1", Time: at(1)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestBackoff (0.01s)\n    backoff_test.go:41: attempt 4 went out after 0s\nFAIL", Time: at(2)},
			{Role: sources.RoleCommand, Text: "go test ./internal/exporter/ -count=1", Time: at(3)},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/exporter\t0.9s", Time: at(4)},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{
		{Command: "$ go test ./internal/exporter/ -run TestBackoff -count=1", Outcome: "recorded"},
		{Command: "$ go test ./internal/exporter/ -count=1", Outcome: "recorded"},
	}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "failed" {
		t.Errorf("the run that printed FAIL came back as %q", data.Tests[0].Outcome)
	}
	if data.Tests[1].Outcome != "passed" {
		t.Errorf("the run that printed ok came back as %q", data.Tests[1].Outcome)
	}
}

// A command whose output nobody recorded stays what it was: the packet does not
// claim a run passed because nothing said otherwise.
func TestAnUnrecordedCommandKeepsItsOutcome(t *testing.T) {
	now := time.Now().UTC()
	session := model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "make deploy", Time: now},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{{Command: "$ make deploy", Outcome: "recorded"}}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "recorded" {
		t.Errorf("an unrecorded run was called %q", data.Tests[0].Outcome)
	}
}

// An exit marker the harness wrote wins: it is the harness's own word on the run,
// and the packet already trusted it.
func TestARecordedExitStatusIsNotOverwritten(t *testing.T) {
	now := time.Now().UTC()
	session := model.Session{
		Harness: "codex", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go test ./... → exit 1", Time: now},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/exporter\t0.9s", Time: now.Add(time.Minute)},
		},
	}
	data := model.CompactionContext{Tests: []model.ContextTest{{Command: "$ go test ./... → exit 1", Outcome: "failed"}}}
	withCommandOutcomes(&data, session)
	if data.Tests[0].Outcome != "failed" {
		t.Errorf("the harness's own exit status was overwritten with %q", data.Tests[0].Outcome)
	}
}
