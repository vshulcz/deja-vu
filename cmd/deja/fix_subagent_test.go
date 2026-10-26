package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A failure and its fix inside a subagent's run, deep enough that the cut keeps
// neither as part of the task or the closing turns. `deja fix` names the fix:
// the cut used to drop every command a child ran, so most failure-and-fix
// episodes on a machine that delegates its edits were never mined (#5678).
func TestFixNamesARemedyASubagentRan(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	sub := filepath.Join(tmp, "claude", "-work-img", "parent-1", "subagents")
	for _, d := range []string{home, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")

	refusal := "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3"
	mirror := "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3"
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	row := func(typ, content string) string {
		return fmt.Sprintf(`{"type":%q,"sessionId":"parent-1","isSidechain":true,"agentId":"child-1","timestamp":%q,"cwd":"/work/img","message":{"role":%q,"content":%s}}`, typ, at, typ, content)
	}
	bash := func(id, cmd, out string, failed bool) []string {
		return []string{
			row("assistant", fmt.Sprintf(`[{"type":"text","text":"running it"},{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]`, id, cmd)),
			row("user", fmt.Sprintf(`[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":%q}]`, id, failed, out)),
		}
	}
	rows := make([]string, 0, 22)
	rows = append(rows, row("user", `"build the vllm image"`))
	for i := range 4 {
		rows = append(rows, bash(fmt.Sprintf("g%d", i), fmt.Sprintf("git log --oneline -1 part%d", i), "abc123 part", false)...)
	}
	rows = append(rows, bash("t1", "pip3 install vllm==0.6.3", refusal, true)...)
	rows = append(rows, bash("t2", mirror, "Successfully installed vllm-0.6.3", false)...)
	for i := range 4 {
		rows = append(rows, bash(fmt.Sprintf("s%d", i), fmt.Sprintf("git status part%d", i), "clean", false)...)
	}
	rows = append(rows, row("assistant", `"the image builds; vllm comes from the mirror"`))
	if err := os.WriteFile(filepath.Join(sub, "agent-child-1.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", "ERROR: No matching distribution found for vllm==0.6.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mirror) {
		t.Errorf("fix does not name the mirror install the subagent ran:\n%s", out)
	}
}
