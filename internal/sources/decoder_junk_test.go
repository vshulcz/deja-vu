package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Malformed and half-typed records of every shape the new decoders touch.
func TestCodexAndCursorSurviveJunk(t *testing.T) {
	dir := t.TempDir()
	codex := []string{
		`{"timestamp":"2026-07-31T00:00:00Z","type":"session_meta","payload":{"session_id":"j","cwd":"/w"}}`,
		`{"timestamp":"x","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"not json"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":null}"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":123}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":null}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":"Output:\n"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":""}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n-orphan removal with no file header\n*** End Patch"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Update File: \n-x"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":123}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"keep one"}]}}`,
	}
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-junk.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(codex, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCodexRollout(p); err != nil {
		t.Fatal(err)
	}

	cdir := filepath.Join(dir, "projects", "Users-me-app", "agent-transcripts", "junk")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cursor := []string{
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":null}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":123}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/a","old_string":null}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":null}}]}}`,
		`{"role":"assistant","message":{"content":"a plain string, not a list"}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"StrReplace","input":{"path":"/a","edits":"not a list"}}]}}`,
		`{"role":"user","message":{"content":[{"type":"text","text":"keep one"}]}}`,
	}
	cp := filepath.Join(cdir, "junk.jsonl")
	if err := os.WriteFile(cp, []byte(strings.Join(cursor, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCursorTranscript(cp); err != nil {
		t.Fatal(err)
	}
}
