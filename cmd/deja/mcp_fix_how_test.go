package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// fixHowStore has two sessions that hit an error and ran a command after it.
// Two rather than one on purpose: the command listing needs a command to have
// been run in at least two sessions before it remembers it at all, so a
// one-session fixture leaves `how` with nothing to say.
func fixHowStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-f")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for _, id := range []string{"f1", "f2"} {
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/f","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the migration keeps failing"}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/f","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -f migrate.sql"}}]}}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/f","timestamp":"2026-07-20T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"psql: FATAL: role app_user does not exist"}]}}` + "\n" +
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/f","timestamp":"2026-07-20T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -f schema.sql"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `fix` and `how` are two of the six tools the MCP server exposes, and neither
// had a test: measured, an agent could have been answered with an empty string
// by both and nothing in the package would have noticed.
func TestMCPFixNamesWhatRanAfterTheError(t *testing.T) {
	dir := fixHowStore(t)

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"psql: FATAL: role app_user does not exist"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("fix answered with nothing")
	}
	if !strings.Contains(got, "ran next:") {
		t.Errorf("the answer does not say what ran next:\n%s", got)
	}
	if !strings.Contains(got, "schema.sql") {
		t.Errorf("the command that followed the error is missing:\n%s", got)
	}
}

// An error nobody hit is answered plainly rather than with silence.
func TestMCPFixSaysWhenNothingFollowedTheError(t *testing.T) {
	dir := fixHowStore(t)

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"FATAL: the widget subsystem refused to start"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("fix answered an unknown error with nothing")
	}
	// Named, not merely non-empty: an assertion that only checks the absence
	// of the other session's command would pass on any string at all.
	if !strings.Contains(got, "No session on this machine ran a command after that error") {
		t.Errorf("the answer does not say that nothing followed it:\n%s", got)
	}
	if strings.Contains(got, "schema.sql") {
		t.Errorf("an unrelated pair was returned:\n%s", got)
	}
}

// The argument is required: without it there is nothing to look up, and an
// empty answer would read as "no session hit that error".
func TestMCPFixRefusesAnEmptyError(t *testing.T) {
	dir := fixHowStore(t)

	if _, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"   "}`)); err == nil {
		t.Error("fix accepted a blank error")
	}
	if _, err := callMCPTool(dir, "fix", json.RawMessage(`{}`)); err == nil {
		t.Error("fix accepted a missing error")
	}
}

func TestMCPHowNamesTheCommandAndHowOftenItRan(t *testing.T) {
	dir := fixHowStore(t)

	got, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"psql"}`))
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("how answered with nothing")
	}
	if !strings.Contains(got, "psql") {
		t.Errorf("the command is missing:\n%s", got)
	}
	// The count, not the word: "session" alone matches an id or a log line.
	if !strings.Contains(got, "2 sessions") {
		t.Errorf("the answer does not say it ran in both sessions:\n%s", got)
	}
}

// A topic this machine never ran is said out loud, not answered with an empty
// string an agent would read as an error.
func TestMCPHowSaysWhenNothingMatches(t *testing.T) {
	dir := fixHowStore(t)

	got, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"kubectl"}`))
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if !strings.Contains(got, "No command on this machine mentions") {
		t.Errorf("an unmatched topic was not named:\n%s", got)
	}
	if !strings.Contains(got, "kubectl") {
		t.Errorf("the answer does not repeat what was asked:\n%s", got)
	}
}

func TestMCPHowRefusesAnEmptyTopic(t *testing.T) {
	dir := fixHowStore(t)

	if _, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"  "}`)); err == nil {
		t.Error("how accepted a blank topic")
	}
	if _, err := callMCPTool(dir, "how", json.RawMessage(`{}`)); err == nil {
		t.Error("how accepted a missing topic")
	}
}
