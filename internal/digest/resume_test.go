package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A compaction costs a median of 28 actions before the next edit against 10 from
// a cold start, over 86 of them on a real machine. Nothing is missing from the
// store when it happens — the shape is. This is the shape, and every part of it
// is derived, because the two places an agent can write hold 9 writes against
// 3793 injections.
func TestTheHandoverIsWhatTheSessionWasDoing(t *testing.T) {
	now := time.Now().UTC()
	at := func(n int) time.Time { return now.Add(time.Duration(n) * time.Minute) }
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			// What a harness wrote is not what was asked.
			{Role: "user", Text: "Summary:\n1. **Primary Request and Intent:** the quokkabloom fetcher", Time: at(0)},
			{Role: "user", Text: "cap the quokkabloom retries and make the cold path warm the index", Time: at(1)},
			{Role: sources.RoleEdit, Text: "internal/fetch/quokkabloom.go", Time: at(2)},
			{Role: sources.RoleCommand, Text: "go test ./internal/fetch -run Quokkabloom", Time: at(3)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestQuokkabloomRetries\npanic: too many retries", Time: at(4)},
			{Role: "assistant", Text: "Decision: the retry budget stays at four and the cold path warms the index first.", Time: at(5)},
			{Role: sources.RoleEdit, Text: "internal/fetch/cold.go", Time: at(6)},
			{Role: sources.RoleCommand, Text: "go test ./internal/fetch -count=1", Time: at(7)},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/fetch\t1.2s", Time: at(8)},
		},
	}
	r := ResumeFrom(s, func(out string) bool { return strings.Contains(out, "FAIL") })
	if strings.Contains(r.Asked, "Primary Request") {
		t.Errorf("a compaction summary is reported as the task: %q", r.Asked)
	}
	if !strings.Contains(r.Asked, "cap the quokkabloom retries") {
		t.Errorf("the task is missing: %q", r.Asked)
	}
	if !strings.Contains(r.Decision, "retry budget stays at four") {
		t.Errorf("what the session settled is missing: %q", r.Decision)
	}
	if len(r.Files) == 0 || r.Files[0] != "internal/fetch/cold.go" {
		t.Errorf("the files in flight are not newest first: %v", r.Files)
	}
	if r.Command != "go test ./internal/fetch -count=1" {
		t.Errorf("the last command is wrong: %q", r.Command)
	}
	if r.Failed {
		t.Error("the last command passed, and the handover says it failed")
	}
	lines := strings.Join(r.Lines(), "\n")
	for _, want := range []string{"working on:", "settled:", "files in flight:", "last command:"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the rendered handover is missing %q:\n%s", want, lines)
		}
	}
}

// A failure is the part that changes what the next turn does first, so it is
// said as one.
func TestAHandoverSaysTheLastCommandFailed(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go build ./..."},
			{Role: sources.RoleToolOutput, Text: "internal/fetch/cold.go:12: undefined: warmIndex"},
		},
	}
	r := ResumeFrom(s, func(out string) bool { return strings.Contains(out, "undefined") })
	if !r.Failed {
		t.Fatal("the failure did not reach the handover")
	}
	if !strings.Contains(strings.Join(r.Lines(), "\n"), "last command failed: go build") {
		t.Errorf("the handover does not say it failed:\n%s", strings.Join(r.Lines(), "\n"))
	}
}

// Nothing to hand over is its own answer: a session with only plumbing in it
// must not produce a line.
func TestAHandoverFromPlumbingIsEmpty(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s3",
		Messages: []model.Message{
			{Role: "user", Text: "<system-reminder>\nCAVEMAN MODE ACTIVE\n</system-reminder>"},
			{Role: "assistant", Text: "File created successfully at: /work/app/notes.txt"},
		},
	}
	if r := ResumeFrom(s, nil); !r.Empty() {
		t.Errorf("plumbing produced a handover: %+v", r)
	}
}
