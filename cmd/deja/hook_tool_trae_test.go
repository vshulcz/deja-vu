package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// traecli 0.208 edits with Claude Code's Edit and Write, and its PreToolUse
// payload names the file only inside command: captured from a live hook,
// {"tool_name":"Edit","tool_input":{"command":"Edit /path"}}. trae-auto has to
// match those tools, and the hook has to find the path there.
func TestTraeEditHookSpeaks(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"Edit", "Write"} {
		b, jerr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       tool,
			"tool_input":      map[string]any{"command": tool + " /work/alpha/config.go"},
			"session_id":      "trae-" + tool,
			"cwd":             "/work/alpha",
		})
		if jerr != nil {
			t.Fatal(jerr)
		}
		out := toolHookRun(t, string(b))
		if !strings.Contains(out, "config.go") {
			t.Errorf("%s on a file with history: %q, want the file's line", tool, out)
		}
	}

	var pre string
	for _, h := range traeHookWiring() {
		if h.Event == "PreToolUse" {
			pre = h.Matcher
		}
	}
	re := regexp.MustCompile("^(?:" + pre + ")$")
	for _, tool := range []string{"Bash", "Edit", "Write"} {
		if !re.MatchString(tool) {
			t.Errorf("trae PreToolUse matcher %q misses %s", pre, tool)
		}
	}
	// Codex's own wiring stays as it was: a changed matcher asks every codex
	// user to trust the hook again.
	for _, h := range codexHookWiring {
		if h.Event == "PreToolUse" && h.Matcher != "Bash|apply_patch" {
			t.Errorf("codex PreToolUse matcher changed to %q", h.Matcher)
		}
	}
}
