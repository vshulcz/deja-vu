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

// Grok Build 1.0.41 shows a SessionStart or UserPromptSubmit hook's
// systemMessage and drops its additionalContext — its hook guide says stdout
// is ignored for SessionStart and discarded for an allowing UserPromptSubmit,
// and the session's chat_history.jsonl carried no deja-recall. deja answered
// both with the full receipt ("1.7 KB of context") and logged them as memory
// that arrived (#4588). Grok runs every command hook with GROK_HOOK_EVENT set.
func TestGrokStartAndPromptWaitForTheFirstToolHook(t *testing.T) {
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
	t.Setenv("GROK_HOOK_EVENT", "")
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
	// Quoted as JSON: a Windows cwd's backslashes are not valid escapes.
	cwdJSON, _ := json.Marshal(cwd)

	// Through the dispatcher, the way grok runs them.
	run := func(name, payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() { _ = runHookDeferred(dir, name, nil, commands[name]) })
	}
	start := func(id string) string {
		return run("hook-context", `{"hookEventName":"session_start","sessionId":"`+id+`","cwd":`+string(cwdJSON)+`,"source":"new","hook_event_name":"SessionStart","session_id":"`+id+`"}`)
	}
	prompt := func(id string) string {
		return run("hook-prompt", `{"hookEventName":"user_prompt_submit","sessionId":"`+id+`","cwd":`+string(cwdJSON)+`,"prompt":"what did we change in the retry_loop in fetcher","hook_event_name":"UserPromptSubmit","session_id":"`+id+`"}`)
	}
	tool := func(id string) string {
		return run("hook-tool", `{"hookEventName":"pre_tool_use","sessionId":"`+id+`","cwd":`+string(cwdJSON)+`,"toolName":"list_dir","toolInput":{}}`)
	}

	t.Setenv("GROK_HOOK_EVENT", "session_start")
	// The receipt still goes out: grok shows the person systemMessage.
	if out := start("grok-1"); strings.Contains(out, "additionalContext") {
		t.Errorf("grok's session start was answered with context it drops: %q", out)
	}
	t.Setenv("GROK_HOOK_EVENT", "user_prompt_submit")
	if out := prompt("grok-1"); strings.Contains(out, "additionalContext") {
		t.Errorf("grok's prompt was answered with context it drops: %q", out)
	}
	// The session's next tool hook is the one grok delivers, with the tool's
	// result, and it carries both.
	t.Setenv("GROK_HOOK_EVENT", "pre_tool_use")
	out := tool("grok-1")
	if !strings.Contains(out, `"hookEventName":"PreToolUse"`) || !strings.Contains(out, "retry_loop") {
		t.Fatalf("grok's first tool hook did not carry the deferred recall: %q", out)
	}
	if again := tool("grok-1"); strings.Contains(again, "retry_loop") {
		t.Fatalf("the deferred recall was delivered twice: %q", again)
	}
	// Another session's tool hook gets none of it.
	if other := tool("grok-2"); strings.Contains(other, "retry_loop") {
		t.Fatalf("one session's deferred recall reached another: %q", other)
	}
	t.Setenv("GROK_HOOK_EVENT", "")

	// The control: outside grok the same start is answered in place.
	if out := start("ctl-1"); !strings.Contains(out, "additionalContext") {
		t.Fatalf("session start delivered nothing outside grok, so this measures nothing: %q", out)
	}
}
