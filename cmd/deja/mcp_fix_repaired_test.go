package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The MCP tool is where an agent reads a remedy with no person in between, so
// it carries the same two halves the CLI does: a repaired remedy is the command
// that failed, corrected, and what makes it the answer is that command.
func TestMCPFixShowsWhatTheRepairedCommandRepaired(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-r")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"r1","cwd":"/w/r","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the migration keeps failing"}}` + "\n" +
		`{"type":"assistant","sessionId":"r1","cwd":"/w/r","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -f migrate.sql"}}]}}` + "\n" +
		`{"type":"user","sessionId":"r1","cwd":"/w/r","timestamp":"2026-07-20T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"psql: FATAL: role app_user does not exist"}]}}` + "\n" +
		`{"type":"assistant","sessionId":"r1","cwd":"/w/r","timestamp":"2026-07-20T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -f migrate.sql --username postgres"}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "r1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"psql: FATAL: role app_user does not exist"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if !strings.Contains(got, "--username postgres") {
		t.Fatalf("the corrected command is missing:\n%s", got)
	}
	if !strings.Contains(got, "after this failed: $ psql -f migrate.sql") {
		t.Errorf("the answer does not say what the command repaired:\n%s", got)
	}
}
