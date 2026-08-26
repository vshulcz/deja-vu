package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja how` exists to hand back a command to run, so the command it prints has
// to be the command that ran. SafeLine collapsed runs of whitespace, and
// `-run "Pool  Size"` came back as `-run "Pool Size"` — a different test filter
// (#2052).
func TestHowPrintsTheCommandThatRan(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	cmd := `go test ./... -run "Pool  Size"`
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "the pgbouncer pool times out"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": cmd}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: how found the command at all. A `grep` would not have been
	// indexed, and an empty answer would pass the check below for free.
	if !strings.Contains(out, "go test") {
		t.Fatalf("how found no command to print:\n%s", out)
	}
	if !strings.Contains(out, cmd) {
		t.Errorf("how printed a command that is not the one that ran:\n%s\nwanted: %s", out, cmd)
	}

	// The same answer through MCP, which is the half an agent runs without a
	// person reading it first.
	got, err := callMCPTool(os.Getenv("DEJA_INDEX_DIR"), "how", json.RawMessage(`{"what":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "go test") {
		t.Fatalf("the MCP tool found no command to print:\n%s", got)
	}
	if !strings.Contains(got, cmd) {
		t.Errorf("the MCP tool handed back a command that is not the one that ran:\n%s\nwanted: %s", got, cmd)
	}
}
