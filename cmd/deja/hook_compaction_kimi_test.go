package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// kimiCompactionSession writes a Kimi Code 0.28.1 session the way the CLI
// lays it out: wire.jsonl under agents/main, the working directory in the
// state.json two levels up. The records are the ones a stand run wrote, with
// the long system prompt and tool catalogue left out.
func kimiCompactionSession(t *testing.T, workspace, id string) {
	t.Helper()
	session := filepath.Join(sources.KimiRoot(), "sessions", "wd_work_50ec6d22c9dc", id)
	state, _ := json.Marshal(map[string]any{
		"createdAt": "2026-10-07T10:16:41.259Z", "updatedAt": "2026-10-07T10:16:57.256Z",
		"title": "fix the parser test", "workDir": workspace,
		"agents": map[string]any{"main": map[string]any{"type": "main", "parentAgentId": nil}},
	})
	compactionWrite(t, filepath.Join(session, "state.json"), string(state))
	compactionWrite(t, filepath.Join(session, "agents", "main", "wire.jsonl"), strings.Join([]string{
		`{"type":"metadata","protocol_version":"1.4","created_at":1791368201286}`,
		`{"type":"config.update","modelAlias":"mock","thinkingEffort":"off","time":1791368201288}`,
		`{"type":"turn.prompt","input":[{"type":"text","text":"fix the parser test"}],"origin":{"kind":"user"},"time":1791368201401}`,
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1791368201402}`,
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1","turnId":"0","step":1},"time":1791368201541}`,
		`{"type":"context.append_loop_event","event":{"type":"tool.call","uuid":"call_1","turnId":"0","step":1,"stepUuid":"s1","toolCallId":"call_1","name":"Bash","args":{"command":"go test ./parser/..."}},"time":1791368201560}`,
		`{"type":"context.append_loop_event","event":{"type":"tool.result","parentUuid":"call_1","toolCallId":"call_1","result":{"output":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nCommand failed with exit code: 1.","isError":true}},"time":1791368201580}`,
		`{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1","turnId":"0","step":1,"finishReason":"tool_use"},"time":1791368201581}`,
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2","turnId":"0","step":2},"time":1791368201590}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}},"time":1791368201600}`,
		`{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","turnId":"0","step":2,"finishReason":"end_turn"},"time":1791368201610}`,
	}, "\n")+"\n")
}

// Kimi Code waits for PreCompact before it compacts, and the payload names
// only the session: no transcript, no harness. The hook entry says whose it is
// (--harness kimi), the session's wire.jsonl is found by its id, and the
// packet rides the next prompt. The payload is the one Kimi 0.28.1 sent on a
// stand.
func TestKimiCompactionIsReadByTheSessionID(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	out := precompactThen(t, dir, workspace, map[string]any{
		"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace,
		"trigger": "auto", "token_count": 216,
	}, "kimi")
	wantCompactionPacket(t, out)
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "kimi" {
		t.Errorf("the capture is filed under %q, want kimi", state.Harness)
	}
}

// Without the harness the payload names nothing deja can read, which is what
// the hook entry before --harness got: the forget and no capture.
func TestKimiCompactionNeedsTheHarnessFromTheHookEntry(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace, "trigger": "auto"})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "")
	if _, found, _ := index.Compaction(dir, id, workspace); found {
		t.Fatal("captured a Kimi session nobody named the harness of")
	}
}

// Kimi runs hook-context --once and hook-prompt on the same prompt, in
// parallel; on a stand the packet came back from both. Only the prompt hook
// carries it.
func TestKimiOnceDigestLeavesThePacketToThePromptHook(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "kimi")
	prompt, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "cwd": workspace, "prompt": "what next"})
	withHookStdin(t, string(prompt))
	if out := captureStdout(t, func() { _ = runHookContextMode(dir, true, true) }); strings.Contains(out, "Compaction context") {
		t.Fatalf("hook-context --once handed the packet out: %s", out)
	}
	wantCompactionPacket(t, hostsPrompt(t, dir, map[string]any{"session_id": id, "cwd": workspace, "prompt": "what next"}))
}

// The kimi-auto block passes --harness kimi on its PreCompact entry.
func TestKimiAutoNamesTheHarnessOnPreCompact(t *testing.T) {
	hermeticEnv(t)
	r, err := installKimiAuto("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readConfig(r.Path)
	if !strings.Contains(string(b), `hook-precompact --harness kimi"`) {
		t.Fatalf("the PreCompact entry does not name kimi:\n%s", b)
	}
}
