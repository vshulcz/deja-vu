package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// ranStoreFor writes sessions that all edit one file and all run one command,
// with the outcome the caller asks for, and returns the built index.
func ranStoreFor(t *testing.T, cmd string, clean bool, sessions int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	for k := range sessions {
		sid := fmt.Sprintf("s%d", k)
		bad := ""
		if !clean {
			bad = `"is_error":true,`
		}
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(300-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"the golden files under internal/store are stale again"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(299-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/internal/store/store.go","old_string":"x","new_string":"y"}}]}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(298-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"` + cmd + `"}}]}}`,
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(297-20*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1",` + bad + `"content":"ok"}]}}`,
			// An inspection command that also passed, which must not be what the
			// line hands over.
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(296-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"git status --short"}}]}}`,
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(295-20*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","content":"ok"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line before an edit said how many sessions had worked on the file and,
// with nothing else to say, offered `deja blame`. What those sessions ran to
// check their own edit was in the index and never reached the agent — and
// finding it again is the expensive part: on a 325-file fixture, ten to sixteen
// reads went into it after the answer had already arrived.
func TestTheFileLineHandsOverTheCommandThatPassedHere(t *testing.T) {
	dir := ranStoreFor(t, "go test -tags golden ./internal/store", true, 5)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if !strings.Contains(line, "go test -tags golden ./internal/store") {
		t.Errorf("the command those sessions ran is not in the line:\n  %s", line)
	}
	if strings.Contains(line, "deja blame") {
		t.Errorf("the pointer was served over the command itself:\n  %s", line)
	}
	if strings.Contains(line, "git status") {
		t.Errorf("an inspection command was handed over as the way to check an edit:\n  %s", line)
	}
	if strings.Count(line, "\n") > 0 {
		t.Errorf("the hook wrote more than one line:\n  %s", line)
	}
}

// The claim is that the command passed, so a command the transcript recorded as
// failing must not become one — the file line falls back to the pointer it
// always had.
func TestAFailingCommandIsNotOfferedAsTheWayToCheckAnEdit(t *testing.T) {
	dir := ranStoreFor(t, "go test -tags golden ./internal/store", false, 5)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if strings.Contains(line, "go test -tags golden") {
		t.Errorf("a command nobody saw pass is offered as one that did:\n  %s", line)
	}
	if !strings.Contains(line, "deja blame") {
		t.Errorf("without evidence the line no longer offers the history:\n  %s", line)
	}
}

// Below the count bar the line exists only if it carries something the agent can
// act on, and two sessions that both ended cleanly on the same command are that.
// A file with no such pattern stays silent rather than printing the count the
// bar exists to withhold.
func TestBelowTheCountBarTheCommandStandsInForADecision(t *testing.T) {
	dir := ranStoreFor(t, "go test -tags golden ./internal/store", true, 2)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if !strings.Contains(line, "go test -tags golden ./internal/store") {
		t.Errorf("two sessions that agree on a passing command said nothing:\n  %s", line)
	}
	dir = ranStoreFor(t, "go test -tags golden ./internal/store", false, 2)
	if line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go"); line != "" {
		t.Errorf("a file with no decision and no evidence still produced a line:\n  %s", line)
	}
}

// The number in the line is sessions, and the manifest is matched on a
// basename — a session that touched two files with the same name comes back
// twice, and counting it twice would put a second session in a sentence that
// has one.
func TestOneSessionIsCountedOnceHoweverOftenItIsHandedOver(t *testing.T) {
	dir := ranStoreFor(t, "go test -tags golden ./internal/store", true, 2)
	metas := index.FileSessions(dir, "/work/app/internal/store/store.go")
	if len(metas) != 2 {
		t.Fatalf("the fixture produced %d sessions", len(metas))
	}
	doubled := append(append([]index.SessionMeta{}, metas...), metas...)
	if got := fileHookRanLine(dir, doubled); !strings.Contains(got, "(2 sessions)") {
		t.Errorf("a doubled list of the same two sessions reads %q", got)
	}
}

// A check that was red in the last two sessions is worth as much as one that
// was green: an agent told nothing runs it, watches it fail, and spends the turn
// working out whether it broke something. What outranks it is a command that
// passed — at any count, because one of the two is a thing to run.
func TestARedCheckIsSaidAndAGreenOneOutranksIt(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	var ss []model.Session
	var metas []index.SessionMeta
	for i, id := range []string{"r1", "r2"} {
		ss = append(ss, model.Session{Harness: "codex", ID: id, Messages: []model.Message{
			{Role: "command", Text: "$ go test ./internal/store  → exit 2"},
		}})
		metas = append(metas, index.SessionMeta{Harness: "codex", ID: id, Updated: at(i)})
	}
	index.BuildSessionFactsForTest(dir, ss)
	got := fileHookRanLine(dir, metas)
	if !strings.Contains(got, "go test ./internal/store") || !strings.Contains(got, "exit 2") {
		t.Errorf("a check red in two sessions is not reported: %q", got)
	}
	if strings.Contains(got, "passed") {
		t.Errorf("a failing command reads as one that passed: %q", got)
	}

	// The same two sessions, both also ending on something that worked: that is
	// the line, and the failure yields to it. Both, because the two-session bar
	// applies to either half — one session's green is still a keystroke.
	for i := range ss {
		ss[i].Messages = append([]model.Message{{Role: "command", Text: "$ make test  → exit 0"}}, ss[i].Messages...)
	}
	index.BuildSessionFactsForTest(dir, ss)
	if got := fileHookRanLine(dir, metas); !strings.Contains(got, "make test") || !strings.Contains(got, "passed") {
		t.Errorf("a command that passed did not outrank the red one: %q", got)
	}
}

// at is a fixed clock for the manifest rows these tests build by hand.
func at(min int) time.Time {
	return time.Date(2026, 9, 1, 10, min, 0, 0, time.UTC)
}
