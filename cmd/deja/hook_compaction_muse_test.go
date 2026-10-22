package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Muse Code fires PreCompact before every compaction with transcript_path null
// and no harness in the payload, and nothing after it that takes context:
// PostCompact rejects additionalContext and SessionStart does not fire again.
// The log is found by the session id, and the packet goes out on the next
// prompt or edit (1.4.1 and 1.4.2, measured, #4737).
func TestMuseCompactionPacketReachesTheNextPrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "01a10bdb-0147-7f40-99e9-cc17071cd6e8"
	rec := func(n int, typ string, payload any) string {
		b := mustMarshal(t, map[string]any{
			"schema_version": 1, "id": "r", "stream": map[string]any{"kind": "session", "id": id},
			"sequence": n, "recorded_at": int64(1_791_200_000_000_000) + int64(n)*1_000_000,
			"record_type": "event", "payload_type": typ, "payload": payload,
		})
		return string(b)
	}
	run := func(n int, ev map[string]any) string {
		return rec(n, "runtime.session", map[string]any{"kind": "run", "event": ev})
	}
	compactionWrite(t, filepath.Join(sources.MuseRoot(), "2026", "10", "05", id, "session.jsonl"), strings.Join([]string{
		rec(1, "runtime.session.metadata", map[string]any{"record": map[string]any{"workspace_root": workspace}}),
		run(2, map[string]any{"kind": "started", "prompt": "fix the parser test"}),
		run(3, map[string]any{"kind": "assistant_tool_calls_committed", "tool_calls": []any{
			map[string]any{"call_id": "call_1", "name": "bash", "args": `{"command":"go test ./parser/..."}`},
		}}),
		run(4, map[string]any{"kind": "tool_result_batch_committed", "results": []any{
			map[string]any{"tool_call_id": "call_1", "text": `{"command":"go test ./parser/...","exit_code":1,"output":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n"}`},
		}}),
		run(5, map[string]any{"kind": "assistant_message_committed", "text": "The parser test fails: want 3, got 4. I decided to fix parse.go next."}),
	}, "\n")+"\n")

	pre := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "trigger": "hard", "session_id": id, "cwd": workspace, "transcript_path": nil, "model_provider": "meta"})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, err := index.Compaction(dir, id, workspace); err != nil || !found {
		t.Fatalf("precompact stored nothing for a Muse session: %v", err)
	}
	got := hostsPrompt(t, dir, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "cwd": workspace, "transcript_path": nil, "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt packet lacks %q: %s", want, got)
		}
	}
}

// Another session's log under the same id is not this one: the reader checks
// the log's own stream.
func TestMuseCompactionRefusesALogOfAnotherSession(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "01a10bdb-0147-7f40-99e9-cc17071cd6e8"
	b := mustMarshal(t, map[string]any{"stream": map[string]any{"kind": "session", "id": "01a10bdb-0000-7000-8000-000000000000"},
		"recorded_at": 1_791_200_000_000_000, "payload_type": "runtime.session",
		"payload": map[string]any{"kind": "run", "event": map[string]any{"kind": "started", "prompt": "someone else's work"}}})
	compactionWrite(t, filepath.Join(sources.MuseRoot(), "2026", "10", "05", id, "session.jsonl"), string(b)+"\n")
	pre := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "trigger": "hard", "session_id": id, "cwd": workspace})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, _ := index.Compaction(dir, id, workspace); found {
		t.Fatal("captured a log whose stream names another session")
	}
}
