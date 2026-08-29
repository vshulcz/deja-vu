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

// The count is what turns a remedy into a decision: "came up before" reads as a
// coincidence, "came up in 12 sessions" reads as a property of this machine.
// friction, the environment block and the plan finding all lead with it; the
// line that arrives at the moment of the failure did not (#2491).
func TestTheAfterHookSaysHowOftenTheErrorCameUp(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := "psql: error: connection to the orders migration database failed: Connection refused"
	now := time.Now().UTC()
	for k := 0; k < 12; k++ {
		at := now.Add(-time.Duration(600-20*k) * time.Minute).Format(time.RFC3339)
		var lines []string
		add := func(role string, content any) {
			b, err := json.Marshal(map[string]any{"type": role, "sessionId": fmt.Sprintf("m%d", k),
				"timestamp": at, "cwd": "/work/app", "message": map[string]any{"role": role, "content": content}})
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
		add("user", fmt.Sprintf("run the orders migration (%d) against the production database", k))
		add("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
			"input": map[string]any{"command": "make migrate-orders"}}})
		add("user", []any{map[string]any{"type": "tool_result", "is_error": true, "content": wall}})
		add("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
			"input": map[string]any{"command": "docker compose up -d db"}}})
		add("user", []any{map[string]any{"type": "tool_result", "content": "db started"}})
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("m%d.jsonl", k)),
			[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	line := fixPairLine(dir, wall)
	if line == "" {
		t.Fatalf("the fixture found no fix pair for the wall")
	}
	if !strings.Contains(line, "docker compose up -d db") {
		t.Errorf("the remedy is missing: %s", line)
	}
	if !strings.Contains(line, "12 sessions") {
		t.Errorf("twelve sessions hit this error and the line does not say so:\n  %s", line)
	}
}
