package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Command Code's shell hooks have no compaction event. Compacting appends a
// compaction record to the transcript and keeps every turn above it, so the
// next PreToolUse, whose payload names the transcript, captures the session as
// it stood and answers with the packet, once.
func TestCommandCodeCompactionIsCaughtUpAtTheNextToolCall(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	root := filepath.Join(t.TempDir(), "cc", "projects")
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	transcript := filepath.Join(root, "-work", "cc-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(workspace)
	turns := []string{
		`{"type":"session","version":3,"id":"cc-1","timestamp":"2026-10-07T10:00:00Z","cwd":` + string(cwd) + `}`,
		`{"type":"message","id":"a1","timestamp":"2026-10-07T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}]}}`,
		`{"type":"message","id":"a2","timestamp":"2026-10-07T10:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"shell_command","input":{"command":"go test ./parser/..."}}]}}`,
		`{"type":"message","id":"a3","timestamp":"2026-10-07T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\n--- FAIL: TestParseSeed"}]}}`,
	}
	write := func(extra ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(append(append([]string{}, turns...), extra...), "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tp, _ := json.Marshal(transcript)
	tool := `{"session_id":"cc-1","transcript_path":` + string(tp) + `,"cwd":` + string(cwd) +
		`,"hook_event_name":"PreToolUse","tool_name":"shell_command","tool_display_name":"SHELL","tool_input":{"command":"go test ./parser/..."}}`
	write()
	if out := runDeferredHook(t, dir, "hook-tool", nil, tool); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session that never compacted got a packet: %s", out)
	}
	write(
		`{"type":"compaction","id":"c1","parentId":"a3","timestamp":"2026-10-07T10:05:00Z","summary":"summary of the work so far","firstKeptEntryId":"a3"}`,
		`{"type":"message","id":"a4","timestamp":"2026-10-07T10:05:01Z","message":{"role":"assistant","content":[{"type":"text","text":"carrying on"}]}}`,
	)
	out := runDeferredHook(t, dir, "hook-tool", nil, tool)
	for _, want := range []string{"Compaction context", "fix the parser test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "carrying on") {
		t.Errorf("the packet was built from turns after the compaction: %s", out)
	}
	if again := runDeferredHook(t, dir, "hook-tool", nil, tool); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
}
