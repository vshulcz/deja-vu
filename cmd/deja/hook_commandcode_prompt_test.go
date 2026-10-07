package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Command Code fires no per-prompt hook, so the question is answered at the
// first tool call after it, out of the transcript its PreToolUse names, and
// once per question.
func TestCommandCodeToolHookAnswersTheNewestQuestion(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(claude, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	ccRoot := filepath.Join(tmp, "cc", "projects")
	t.Setenv("DEJA_COMMANDCODE_ROOT", ccRoot)
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for i, text := range []string{
		"the retry_loop in fetcher drops the last attempt",
		"unrelated work about deployments and dashboards",
	} {
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":%q,"cwd":%q,"message":{"role":"user","content":%q}}`,
			i, at, filepath.Join(tmp, "proj"), text)
		if err := os.WriteFile(filepath.Join(claude, "proj", fmt.Sprintf("s%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	transcript := filepath.Join(ccRoot, "-proj", "cc-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, _ := json.Marshal(cwd)
	lines := []string{
		`{"type":"session","id":"cc-1","cwd":` + string(cwdJSON) + `}`,
		`{"type":"message","timestamp":"2026-10-07T10:00:00Z","message":{"role":"user","content":"what did we change in the retry_loop in fetcher"}}`,
		`{"type":"message","timestamp":"2026-10-07T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read_file","input":{"path":"fetcher.go"}}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tpJSON, _ := json.Marshal(transcript)
	tool := `{"session_id":"cc-1","transcript_path":` + string(tpJSON) + `,"cwd":` + string(cwdJSON) +
		`,"hook_event_name":"PreToolUse","tool_name":"read_file","tool_display_name":"READ","tool_input":{"path":"fetcher.go"}}`
	got := runDeferredHook(t, dir, "hook-tool", nil, tool)
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil || resp.HookSpecificOutput.HookEventName != "PreToolUse" ||
		!strings.Contains(resp.HookSpecificOutput.AdditionalContext, "retry_loop") {
		t.Fatalf("the tool hook did not answer the question: %q (%v)", got, err)
	}
	if again := runDeferredHook(t, dir, "hook-tool", nil, tool); strings.Contains(again, "retry_loop") {
		t.Fatalf("the same question was answered twice: %q", again)
	}
}
