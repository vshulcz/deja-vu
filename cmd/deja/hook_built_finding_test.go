package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// builtFindingWall is recorded with zsh's position prefix, which the index
// strips, so the note shows the error as it was hit in either shell.
const builtFindingWall = "zsh:1: command not found: timeout"

// seedBuiltFindingStore writes claude sessions, and with wall set two claude
// sessions and one codex session that hit the same error, then builds.
func seedBuiltFindingStore(t *testing.T, wall bool, asked string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	claude := filepath.Join(tmp, "claude", "-work-app")
	user := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":` + mustJSON(t, text) + `}}`
	}
	tool := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":` + mustJSON(t, text) + `}]}}`
	}
	for i, at := range []string{"2026-08-02T10:00:00Z", "2026-08-09T10:00:00Z", "2026-08-16T10:00:00Z"} {
		id := "s" + string(rune('a'+i))
		lines := []string{user(id, at, "wire the release script for build "+id)}
		if asked != "" && i < 2 {
			lines = append(lines, user(id, at, asked))
		}
		if wall && i < 2 {
			lines = append(lines, tool(id, at, builtFindingWall))
		}
		writeClaudeFixture(t, filepath.Join(claude, id+".jsonl"), id, lines)
	}
	if wall {
		codex := filepath.Join(tmp, "codex", "sessions", "2026", "08", "20", "rollout-2026-08-20T09-00-00-cx1.jsonl")
		if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
			t.Fatal(err)
		}
		body := strings.Join([]string{
			`{"type":"session_meta","timestamp":"2026-08-20T09:00:00Z","payload":{"id":"cx1","session_id":"cx1","cwd":"/work/app"}}`,
			`{"type":"message","timestamp":"2026-08-20T09:00:01Z","payload":{"role":"user","content":"tag the release"}}`,
			`{"timestamp":"2026-08-20T09:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{\"command\":[\"git\",\"tag\"]}"}}`,
			`{"timestamp":"2026-08-20T09:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":` + mustJSON(t, builtFindingWall) + `}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(codex, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sessionStartNote runs the session-start hook the way Claude Code does and
// returns what it shows the person.
func sessionStartNote(t *testing.T, dir string) string {
	t.Helper()
	cwd := filepath.Join(t.TempDir(), "work", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	payload := mustMarshal(t, map[string]any{"session_id": "first", "cwd": cwd, "source": "startup"})
	withHookStdin(t, string(payload))
	out := captureStdout(t, func() { _ = runHookContext(dir, false) })
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, out)
	}
	return resp.SystemMessage
}

// The first session after install said only counts; the one concrete thing
// the history holds was printed by `deja install` alone, which a marketplace
// install never runs (#4616).
func TestTheFirstSessionNamesTheErrorThisMachineKeepsHitting(t *testing.T) {
	dir := seedBuiltFindingStore(t, true, "")
	msg := sessionStartNote(t, dir)
	if !strings.Contains(msg, "deja indexed 4 sessions (2 agents)") {
		t.Fatalf("no built note, so nothing was measured:\n%s", msg)
	}
	want := "this machine has hit `command not found: timeout` in 3 sessions (claude, codex)"
	if !strings.Contains(msg, want) {
		t.Fatalf("the first session does not name the error and its agents:\n%s\nwant a line %q", msg, want)
	}
	checkPersonLines(t, "the built note", builtNoteLines(msg), 2)
}

// builtNoteLines is the built note out of a session start's message.
func builtNoteLines(msg string) string {
	i := strings.Index(msg, "deja indexed")
	if i < 0 {
		return ""
	}
	return msg[i:]
}

// A history with no finding keeps the note it had.
func TestTheBuiltNoteWithoutAFindingIsUnchanged(t *testing.T) {
	dir := seedBuiltFindingStore(t, false, "")
	got := builtNote(dir)
	if want := builtLine(3, 1, 0, true); got != want {
		t.Fatalf("built note = %q, want %q", got, want)
	}
}

// With no error to name, the note quotes a question asked again a week later,
// but only to the host's own UI. `hook-context --notes` feeds plugin replies,
// and OpenClaw's /deja answers in group chats too.
func TestTheBuiltNoteQuotesAQuestionOnlyToTheOwner(t *testing.T) {
	const asked = "why does the release script drop the changelog footer"
	dir := seedBuiltFindingStore(t, false, asked)
	shared := humanNotesFor(dir, false)
	if !strings.HasPrefix(shared, "deja indexed 3 sessions") {
		t.Fatalf("no built note on --notes, so nothing was measured: %q", shared)
	}
	if strings.Contains(shared, "release script drop") || strings.Contains(shared, "\n") {
		t.Fatalf("the notes a plugin may post to a group quote the person's question:\n%s", shared)
	}

	dir = seedBuiltFindingStore(t, false, asked)
	own := humanNotes(dir)
	want := `you asked "` + asked + `" in 2 sessions (claude)`
	if !strings.HasSuffix(own, "\n"+want) {
		t.Fatalf("the owner's note does not quote the repeated question:\n%s\nwant %q", own, want)
	}
	checkPersonLines(t, "the built note", own, 2)
}

// Both finding lines stay in one note line however long the error or the
// question, and a cut error keeps both of its ends.
func TestTheBuiltFindingFitsANoteLine(t *testing.T) {
	long := "Error: connect ECONNREFUSED while pulling registry.internal.example.com/team/platform/service-image:2026.10.14-build.1234 from mirror: connection refused"
	agents := []string{"claude", "codex", "cursor", "gemini", "opencode", "copilot"}
	for name, line := range map[string]string{
		"error":     builtErrorLine(long, 123456, agents),
		"cjk error": builtErrorLine(strings.Repeat("错误：无法连接到数据库服务器", 8), 12, agents[:2]),
		"question":  builtAskedLine(strings.Repeat("why does the nightly exporter drop rows ", 5), 4, agents),
		"short":     builtErrorLine(builtFindingWall, 3, agents[:2]),
	} {
		checkPersonLines(t, name, line, 1)
		if !strings.Contains(line, "sessions (") {
			t.Errorf("%s lost its count and agents: %q", name, line)
		}
	}
	if got := builtErrorLine(builtFindingWall, 3, nil); strings.Contains(got, "()") || !strings.HasSuffix(got, " in 3 sessions") {
		t.Errorf("sessions with no agent named left empty parentheses: %q", got)
	}
	got := builtErrorLine(long, 3, agents[:2])
	if !strings.Contains(got, "Error: connect") || !strings.Contains(got, "connection refused`") || !strings.Contains(got, "…") {
		t.Errorf("the cut error lost an end: %q", got)
	}
	if w := termwidth.Columns(got); w < noteWidth-2 {
		t.Errorf("the cut error left %d columns unused: %q", noteWidth-w, got)
	}
}
