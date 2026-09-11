package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// longWorkerCommand is past the block's width on purpose: the remedy recorded
// for a missing wrapper is a real command from real work, and on this machine
// every one of them is far longer than the ninety-six columns the block quotes
// into. That is why the wall with 34 sessions behind it had no remedy line.
const longWorkerCommand = "launchctl kickstart -k system/app.worker && launchctl print system/app.worker | grep -E 'state|pid' | head -5"

// seedWrapperWall writes the same refusal in three projects, each session
// running the wrapped command, being told the wrapper is missing, and then
// running the same command without it.
func seedWrapperWall(t *testing.T, remedy func(i int) string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 3 {
		project := fmt.Sprintf("app%d", i)
		store := filepath.Join(root, "-work-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("w%d", i)
		rec := func(role string, content any, at string) string {
			b, err := json.Marshal(map[string]any{
				"type": role, "sessionId": id, "cwd": "/work/" + project, "timestamp": at,
				"message": map[string]any{"role": role, "content": content},
			})
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		lines := []string{
			rec("user", "restart the worker", "2026-04-0"+fmt.Sprint(i+1)+"T10:00:00Z"),
			rec("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
				"input": map[string]any{"command": "timeout 12 " + longWorkerCommand}}},
				"2026-04-0"+fmt.Sprint(i+1)+"T10:00:10Z"),
			rec("user", []any{map[string]any{"type": "tool_result", "is_error": true,
				"content": "zsh:1: command not found: timeout"}}, "2026-04-0"+fmt.Sprint(i+1)+"T10:00:20Z"),
			rec("assistant", []any{map[string]any{"type": "tool_use", "name": "Bash",
				"input": map[string]any{"command": remedy(i)}}}, "2026-04-0"+fmt.Sprint(i+1)+"T10:00:30Z"),
			rec("user", []any{map[string]any{"type": "tool_result", "content": "ok"}},
				"2026-04-0"+fmt.Sprint(i+1)+"T10:00:40Z"),
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The most frequent wall on this machine — `command not found: timeout`, 34
// sessions — carried no remedy line, because every pair recorded for it is a
// long command from other work and the block drops anything past its width.
// What those pairs have in common is short enough to say.
func TestTheEnvironmentBlockSaysTheCommandRanWithoutTheWrapper(t *testing.T) {
	dir := seedWrapperWall(t, func(int) string { return longWorkerCommand })
	block, _ := environmentBlockFrom(dir, "auto")
	if !strings.Contains(block, "command not found: timeout") {
		t.Fatalf("the wall itself is missing from the block:\n%s", block)
	}
	if !strings.Contains(block, "the same command without `timeout`") {
		t.Errorf("the remedy every recorded pair agrees on is missing:\n%s", block)
	}
}

// And when the pairs are something else, the wall stays a fact: the block does
// not invent a remedy out of a command that merely followed.
func TestTheEnvironmentBlockDoesNotInventAWrapperRemedy(t *testing.T) {
	dir := seedWrapperWall(t, func(i int) string {
		return fmt.Sprintf("launchctl print system/app.worker | grep state%d", i)
	})
	block, _ := environmentBlockFrom(dir, "auto")
	if strings.Contains(block, "the same command without") {
		t.Errorf("a remedy was claimed that no pair supports:\n%s", block)
	}
}
