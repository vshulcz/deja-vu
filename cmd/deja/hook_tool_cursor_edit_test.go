package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cursor's preToolUse sends its own names: an edit is StrReplace, Write or
// Delete, and the file is under `path`. The hook knew Edit and file_path only,
// so a Cursor agent never got the file's history before changing it (#4191).
func TestToolHookSpeaksForACursorEdit(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "cu" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit config"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The control: Claude's names on the same file and index.
	if out := toolHookRun(t, `{"hook_event_name":"preToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"conversation_id":"c0","workspace_roots":["/work/alpha"]}`); !strings.Contains(out, "config.go") {
		t.Fatalf("the control produced no file line: %q", out)
	}
	for _, payload := range []string{
		`{"hook_event_name":"preToolUse","tool_name":"StrReplace","tool_input":{"path":"/work/alpha/config.go","old_string":"a","new_string":"b"},"conversation_id":"c1","workspace_roots":["/work/alpha"]}`,
		`{"hook_event_name":"preToolUse","tool_name":"Write","tool_input":{"path":"/work/alpha/config.go","contents":"package alpha\n"},"conversation_id":"c2","workspace_roots":["/work/alpha"]}`,
		`{"hook_event_name":"preToolUse","tool_name":"Delete","tool_input":{"path":"/work/alpha/config.go"},"conversation_id":"c3","workspace_roots":["/work/alpha"]}`,
	} {
		if out := toolHookRun(t, payload); !strings.Contains(out, "config.go") {
			t.Errorf("no file line for %s: %q", payload, out)
		}
	}
	// Cursor's Read stays silent, as Claude's does: reading is not changing.
	if out := toolHookRun(t, `{"hook_event_name":"preToolUse","tool_name":"Read","tool_input":{"path":"/work/alpha/config.go"},"conversation_id":"c4","workspace_roots":["/work/alpha"]}`); out != "" {
		t.Errorf("a Cursor Read produced a line: %q", out)
	}
}
