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

// A program this machine does not have is missing in every checkout, so the
// command that worked without it answers in all of them, wherever it was
// learned. Another project's next command still does not: that is what the
// project did next, not a fact about the machine.
func TestAMissingProgramIsAnsweredFromAnyProject(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-billing")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	wall := "zsh:1: command not found: timeout"
	other := "psql: error: connection to the billing database failed: Connection refused"
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
		bash := func(cmd string) {
			add("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": cmd}}})
		}
		result := func(text string, failed bool) {
			add("user", []any{map[string]any{"type": "tool_result", "is_error": failed, "content": text}})
		}
		add("user", fmt.Sprintf("run the billing checks (%d)", k))
		bash("timeout 120 make check-billing")
		result(wall, true)
		bash("make check-billing")
		result("all checks passed", false)
		bash("make migrate-billing")
		result(other, true)
		bash("docker compose up -d db")
		result("db started", false)
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("b%d.jsonl", k)),
			[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if line := fixPairLine(dir, "/work/storefront", wall); !strings.Contains(line, "the same command worked as: make check-billing") {
		t.Errorf("a missing program was not answered from another project: %q", line)
	}
	if line := fixPairLine(dir, "/work/storefront", other); line != "" {
		t.Errorf("another project's next command reached this one: %q", line)
	}
}
