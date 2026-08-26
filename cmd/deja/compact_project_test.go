package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The compaction block is a session's own evidence, and "its own" is the whole
// point: the hook payload names no harness, so a shared id used to resolve to
// whichever copy the index had seen most recently — which can be a different
// project's session that happens to have been touched later (#1999).
func TestCompactEvidenceStaysInTheCallersProject(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	codex := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(filepath.Join(claude, "-w-other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// The other project's copy, and the newer of the two in the index.
	other := rec(map[string]any{
		"type": "user", "sessionId": "shared", "cwd": "/w/other",
		"timestamp": "2026-06-01T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "the other project's question"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "shared", "cwd": "/w/other",
		"timestamp": "2026-06-01T10:02:00Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/w/other/claude_only.go"}},
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "go test ./pkg/ -run ClaudeOnly"}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(claude, "-w-other", "shared.jsonl"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	// The session that is compacting: same id, this project, older.
	mine := `{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"shared","cwd":"/w/app"}}` + "\n" +
		`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"the app's question about pgbouncer"}}` + "\n" +
		`{"timestamp":"2026-01-01T12:00:02Z","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test ./app/ -run Pool\"}"}}` + "\n"
	if err := os.WriteFile(filepath.Join(codex, "rollout-2026-01-01T12-00-00-shared.jsonl"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	// The premise: without a project to go on, the newer copy is what answers.
	// If that stops being true the case below passes for the wrong reason.
	if blind := compactEvidence(dir, "shared", ""); !strings.Contains(blind, "claude_only.go") {
		t.Fatalf("the fixture does not reproduce the mix-up it is about:\n%s", blind)
	}

	got := compactEvidence(dir, "shared", "/w/app")
	if got == "" {
		t.Fatal("the compacting session got no block at all")
	}
	if strings.Contains(got, "claude_only.go") || strings.Contains(got, "ClaudeOnly") {
		t.Errorf("the block names another project's work:\n%s", got)
	}
	if !strings.Contains(got, "run Pool") {
		t.Errorf("the block does not name what this session actually ran:\n%s", got)
	}
}
