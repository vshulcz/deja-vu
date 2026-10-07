package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Cline CLI tells its plugin a compaction is starting through onEvent and does
// not wait for the answer, but messages.json keeps every turn through it, the
// summary going to <id>.compaction.json (CLI 3.0.69, measured). The session is
// found by its id; the shapes are the ones that stand wrote.
func TestClineCompactionIsReadFromMessagesJSON(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "1791367873575_myzey"
	session := filepath.Join(sources.ClineSessionsDir(), id)
	manifest, _ := json.Marshal(map[string]any{
		"version": 1, "session_id": id, "source": "cli", "started_at": "2026-10-07T10:11:13.579Z",
		"status": "completed", "provider": "openai-compatible", "model": "stub-model",
		"cwd": workspace, "workspace_root": workspace, "prompt": "fix the parser test",
		"messages_path": filepath.Join(session, id+".messages.json"),
	})
	compactionWrite(t, filepath.Join(session, id+".json"), string(manifest))
	messages, _ := json.Marshal(map[string]any{
		"version": 1, "updated_at": "2026-10-07T10:11:16.423Z", "agent": "lead", "sessionId": id,
		"origin": map[string]any{"source": "cli", "mode": "user", "sessionId": id, "version": "3.0.69"},
		"messages": []any{
			map[string]any{"id": "u1", "role": "user", "ts": 1791367874407, "content": []any{map[string]any{"type": "text", "text": `<user_input mode="act">fix the parser test</user_input>`}}},
			map[string]any{"id": "a1", "role": "assistant", "ts": 1791367874802, "content": []any{map[string]any{"type": "tool_use", "id": "call_1", "name": "run_commands", "input": map[string]any{"commands": []any{"go test ./parser/..."}}}}},
			map[string]any{"id": "r1", "role": "user", "ts": 1791367875300, "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "name": "run_commands", "content": []any{
				map[string]any{"query": "go test ./parser/...", "result": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n", "success": false},
			}}}},
			map[string]any{"id": "a2", "role": "assistant", "ts": 1791367875900, "content": []any{map[string]any{"type": "text", "text": "The parser test fails: want 3, got 4. I decided to fix parse.go next."}}},
		},
	})
	compactionWrite(t, filepath.Join(session, id+".messages.json"), string(messages))
	out := precompactThen(t, dir, workspace, map[string]any{"session_id": id, "cwd": workspace, "harness": "cline"}, "")
	wantCompactionPacket(t, out)
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "cline" {
		t.Errorf("the capture is filed under %q, want cline", state.Harness)
	}
}

// The plugin answers the status notice that opens a compaction, names the
// session and the harness, and asks the prompt hook again so the packet goes
// into the next request of the same run.
func TestClinePluginCapturesOnCompactionStart(t *testing.T) {
	js := clinePluginJS("/bin/deja")
	for _, want := range []string{
		`meta.kind.endsWith("compaction") && meta.phase === "started"`,
		`run(["hook-precompact"], JSON.stringify({ session_id: session, cwd: process.cwd(), harness: "cline" }))`,
		`if (prompt !== asked || compacted) {`,
		`"hooks"]`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("cline plugin lacks %q", want)
		}
	}
}
