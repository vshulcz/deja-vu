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

// What the shell says about a command line is the same for every command
// that trips it, so it does not make two failures the same: the hint goes to
// the command that failed that way before, not to any command that hit the
// same parse error. A missing program the command runs is still answered.
func TestAShellComplaintAloneDoesNotMakeFailuresTheSame(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-orchard")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	parse := "(eval):1: == not found"
	missing := "zsh:1: command not found: timeout"
	now := time.Now().UTC()
	for k := range 2 {
		at := now.Add(-time.Duration(600-20*k) * time.Minute).Format(time.RFC3339)
		var lines []string
		add := func(role string, content any) {
			b, err := json.Marshal(map[string]any{"type": role, "sessionId": fmt.Sprintf("o%d", k),
				"timestamp": at, "cwd": "/work/orchard", "message": map[string]any{"role": role, "content": content}})
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
		add("user", fmt.Sprintf("lint the orchard planner (%d)", k))
		bash("make lint-planner && echo == planner done")
		result(parse, true)
		bash("make lint-planner && echo planner done")
		result("planner done", false)
		bash("timeout 120 make check-orchard")
		result(missing, true)
		bash("make check-orchard")
		result("all checks passed", false)
		if err := os.WriteFile(filepath.Join(store, fmt.Sprintf("o%d.jsonl", k)),
			[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	const fixed = "make lint-planner && echo planner done"
	if line := fixPairLineFor(dir, "/work/orchard", "cd /work/orchard && make lint-planner && echo == planner ok", parse); !strings.Contains(line, fixed) {
		t.Errorf("the command that failed this way before got no hint: %q", line)
	}
	if line := fixPairLineFor(dir, "/work/orchard", "go test ./internal/rows; echo == rows end", parse); line != "" {
		t.Errorf("another command that hit the same parse error got this one's fix: %q", line)
	}
	if line := fixPairLineFor(dir, "/work/orchard", "", parse); !strings.Contains(line, fixed) {
		t.Errorf("with no command to compare, the pair went missing: %q", line)
	}
	if line := fixPairLineFor(dir, "/work/orchard", "timeout 30 go vet ./...", missing); !strings.Contains(line, "make check-orchard") {
		t.Errorf("a missing program the command runs was not answered: %q", line)
	}
	if line := fixPairLineFor(dir, "/work/orchard", "./scripts/sweep-rows.sh", missing); line != "" {
		t.Errorf("a program another command could not find was answered as this one's: %q", line)
	}
}
