package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An edit the harness refused changed nothing: Claude Code answers it with an
// error result (a denied call, a file not read first, text not found), Codex
// with a failing exit code. Read back as a change, the session showed code it
// never wrote.
func TestChangesLeaveOutEditsThatFailed(t *testing.T) {
	write := func(lines ...string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	paths := func(cs []Change) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Path+":"+c.New)
		}
		return strings.Join(out, " ")
	}

	const id = "aaaa0001-1111-4000-8000-d6e7f8a9b0c1"
	edit := func(call, file, newText string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/w","message":{"content":[{"type":"tool_use","id":"` + call + `","name":"Edit","input":{"file_path":"/w/` + file + `","old_string":"a","new_string":"` + newText + `"}}]}}`
	}
	result := func(call string, isErr bool, text string) string {
		e := "false"
		if isErr {
			e = "true"
		}
		return `{"type":"user","sessionId":"` + id + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + call + `","is_error":` + e + `,"content":"` + text + `"}]}}`
	}
	claude := write(
		edit("t1", "denied.go", "NEVER"), result("t1", true, "Permission for this action was denied"),
		edit("t2", "kept.go", "b"), result("t2", false, "The file has been updated."),
		edit("t3", "unanswered.go", "c"),
	)
	if got := paths(Changes(model.Session{Harness: "claude", ID: id, Path: claude})); got != "kept.go:b unanswered.go:c" {
		t.Errorf("claude: %q", got)
	}

	rec := func(typ string, payload any) string {
		b, _ := json.Marshal(map[string]any{"type": typ, "payload": payload})
		return string(b)
	}
	patch := func(file, newText string) string {
		return "*** Begin Patch\n*** Update File: " + file + "\n@@\n-a\n+" + newText + "\n*** End Patch\n"
	}
	shellArgs, _ := json.Marshal(map[string]any{"command": []string{"apply_patch", patch("old.go", "NEVER")}})
	codex := write(
		rec("session_meta", map[string]any{"id": "s", "cwd": "/w"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "apply_patch", "input": patch("bad.go", "NEVER")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": "Exit code: 1\nWall time: 0.1 seconds\nOutput:\napply_patch verification failed: Failed to find expected lines"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c2", "name": "apply_patch", "input": patch("good.go", "b")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c2", "output": "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess. Updated the following files:\nM good.go\n"}),
		rec("response_item", map[string]any{"type": "custom_tool_call", "call_id": "c4", "name": "apply_patch", "input": patch("echo.go", "d")}),
		rec("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "c4", "output": "Success. Updated the following files:\nM echo.go\n+echo \"Exit code: 1\"\n"}),
		rec("response_item", map[string]any{"type": "function_call", "call_id": "c3", "name": "shell", "arguments": string(shellArgs)}),
		rec("response_item", map[string]any{"type": "function_call_output", "call_id": "c3", "output": `{"output":"error: patch failed","metadata":{"exit_code":1}}`}),
	)
	if got := paths(Changes(model.Session{Harness: "codex", ID: "s", Path: codex})); got != "good.go:b echo.go:d" {
		t.Errorf("codex: %q", got)
	}
}
