package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A Copilot Chat edit reaches restore, files, the after-compaction block and
// blame: the replaced side is read from the call's arguments (#595).
func TestCopilotChatEditReachesTheEditSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := editSurfaceRepo(t, tmp)
	root := filepath.Join(tmp, "vscode")
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	file := filepath.ToSlash(filepath.Join(repo, "internal", "pool", "pool.go"))
	const span = "func (p *Pool) Acquire() (*Conn, error) { return p.next(), nil }"
	args, err := json.Marshal(map[string]any{"filePath": file, "oldString": span,
		"newString": "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) { return p.wait(ctx) }"})
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1790000000000)
	state := map[string]any{
		"version": 3, "sessionId": "copilot-chat-edit", "creationDate": now,
		"requests": []any{map[string]any{
			"timestamp": now,
			"message":   map[string]any{"text": "the frobnicator pool needs an acquire timeout"},
			"response": []any{
				map[string]any{"value": "Added a context to Acquire."},
				map[string]any{"kind": "textEditGroup", "uri": map[string]any{"path": file},
					"edits": []any{[]any{map[string]any{"text": "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) { return p.wait(ctx) }"}}}},
			},
			"responseTimestamp": now + 60000,
			"result": map[string]any{"metadata": map[string]any{
				"toolCallRounds": []any{map[string]any{"toolCalls": []any{
					map[string]any{"name": "replace_string_in_file", "id": "c1", "arguments": string(args)},
				}}},
			}},
		}},
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "workspaceStorage", "h1", "chatSessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copilot-chat-edit.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	assertEditSurfaces(t, "copilot-chat", "copilot-chat-edit", file, span, "frobnicator")
}
