package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// What another project ran after the same error is what that project did next,
// and almost never about this failure: over 396 hints on one machine, 2 of 67
// from another project addressed the error and 59 had nothing to do with it.
// The line keeps to the project the agent is in; a pair with no project is a
// fact about the machine and still answers everywhere.
func TestTheAfterHookKeepsToThisProjectsRemedies(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-billing")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := "psql: error: connection to the billing database failed: Connection refused"
	now := time.Now().UTC()
	for k := 0; k < 2; k++ {
		at := now.Add(-time.Duration(600-20*k) * time.Minute).Format(time.RFC3339)
		var lines []string
		add := func(role string, content any) {
			b, err := json.Marshal(map[string]any{"type": role, "sessionId": fmt.Sprintf("b%d", k),
				"timestamp": at, "cwd": "/work/billing", "message": map[string]any{"role": role, "content": content}})
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
		add("user", fmt.Sprintf("start the billing worker (%d)", k))
		add("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
			"input": map[string]any{"command": "make migrate-billing"}}})
		add("user", []any{map[string]any{"type": "tool_result", "is_error": true, "content": wall}})
		add("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
			"input": map[string]any{"command": "docker compose up -d db"}}})
		add("user", []any{map[string]any{"type": "tool_result", "content": "db started"}})
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("b%d.jsonl", k)),
			[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if line := fixPairLine(dir, "/work/billing", wall); !strings.Contains(line, "docker compose up -d db") {
		t.Fatalf("the project's own remedy is missing: %q", line)
	}
	if line := fixPairLine(dir, "/work/storefront", wall); line != "" {
		t.Errorf("another project's next command reached this one: %q", line)
	}
	if line := fixPairLine(dir, "", wall); !strings.Contains(line, "docker compose up -d db") {
		t.Errorf("with no directory to go by the line went silent: %q", line)
	}
	// The hook reads the directory from its payload.
	for cwd, want := range map[string]bool{"/work/billing": true, "/work/storefront": false} {
		payload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Bash",`+
			`"tool_input":{"command":"make migrate-billing"},"tool_response":{"stderr":%q},`+
			`"cwd":%q,"session_id":"s-%s"}`, wall, cwd, filepath.Base(cwd))
		var out bytes.Buffer
		if err := runHookToolAfter(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "docker compose up -d db"); got != want {
			t.Errorf("hook from %s: remedy shown %v, want %v:\n%s", cwd, got, want, out.String())
		}
	}
}
