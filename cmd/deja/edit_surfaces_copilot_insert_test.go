package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Copilot agent transcript's insert_edit_into_file reaches blame through
// what it wrote (#595).
func TestCopilotInsertEditIsAttributedByBlame(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := editSurfaceRepo(t, tmp)
	root := filepath.Join(tmp, "vscode")
	t.Setenv("DEJA_COPILOT_CHAT_ROOTS", root)
	file := filepath.Join(repo, "internal", "pool", "pool.go")
	const line = "deadline, cancel := context.WithTimeout(ctx, p.acquireTimeout)"
	args, err := json.Marshal(map[string]any{"explanation": "Bound Acquire by a timeout.",
		"filePath": filepath.ToSlash(file), "code": "// ...existing code...\n\t" + line + "\n// ...existing code..."})
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session.start","data":{"sessionId":"copilot-insert","startTime":"2026-09-14T05:46:01.086Z"},"timestamp":"2026-09-14T05:46:01.086Z"}`,
		`{"type":"user.message","data":{"content":"the frobnicator pool needs an acquire timeout"},"timestamp":"2026-09-14T05:46:10.000Z"}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"insert_edit_into_file","arguments":` + string(args) + `},"timestamp":"2026-09-14T05:46:13.000Z"}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"c1","success":true},"timestamp":"2026-09-14T05:46:14.000Z"}`,
	}
	dir := filepath.Join(root, "workspaceStorage", "h1", "GitHub.copilot-chat", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copilot-insert.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertWrittenBlame(t, "copilot-insert", file, line)
}
