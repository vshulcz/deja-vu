package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `cd /work/lib && apply_patch` edits /work/lib/<header>, so the pre-edit file
// line has to look that file up, not the header's name in the project the
// session runs in (#5306).
func TestToolHookShellApplyPatchFollowsALeadingCD(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "lib", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/lib","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/lib","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/lib/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	run := func(session, cmd string) string {
		b, jerr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": cmd}, "session_id": session, "cwd": "/work/alpha",
		})
		if jerr != nil {
			t.Fatal(jerr)
		}
		return toolHookRun(t, string(b))
	}
	patch := "apply_patch <<'EOF'\n*** Begin Patch\n*** Update File: config.go\n@@\n+x\n*** End Patch\nEOF"
	if out := run("now", "cd /work/lib && "+patch); !strings.Contains(out, "config.go has been worked on in 6 sessions") {
		t.Fatalf("cd /work/lib && apply_patch got no file line for /work/lib/config.go: %q", out)
	}
	// Control: without the cd the header is /work/alpha/config.go, which no
	// session touched.
	if out := run("now2", patch); out != "" {
		t.Errorf("apply_patch on /work/alpha/config.go spoke: %q", out)
	}
}
