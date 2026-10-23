package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// CodeBuddy Code: PreCompact names the transcript, then SessionStart fires
// with source "compact" — and CodeBuddy drops that hook's context, injecting
// SessionStart context only while the input holds a single user message
// (isFirstOrResumeMessage, 2.161.2). The prompt after it does arrive, so the
// packet waits for UserPromptSubmit rather than being marked delivered into
// nothing (#4705).
func TestCodeBuddyCompactionPacketWaitsForThePrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	ws := mustMarshal(t, workspace)
	transcript := filepath.Join(sources.CodeBuddyRoot(), "-work-app", "cb-1.jsonl")
	compactionWrite(t, transcript, strings.ReplaceAll(strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser test"}],"sessionId":"cb-1","cwd":WS}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"go test ./parser/...\"}","sessionId":"cb-1","cwd":WS}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Bash","callId":"call_1","status":"completed","output":{"type":"text","text":"Command: go test ./parser/...\nStdout: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nStderr: (empty)\nExit Code: 1\nSignal: (none)"},"sessionId":"cb-1","cwd":WS}`,
		`{"id":"m2","timestamp":1791187203000,"type":"message","role":"assistant","content":[{"type":"output_text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"sessionId":"cb-1","cwd":WS}`,
	}, "\n")+"\n", "WS", string(ws)))

	pre := mustMarshal(t, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "cwd": workspace, "hook_event_name": "PreCompact", "trigger": "manual"})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, err := index.Compaction(dir, "cb-1", workspace); err != nil || !found {
		t.Fatalf("precompact stored nothing: %v", err)
	}

	// CodeBuddy's SessionStart payload carries no cwd; the hook runs in it.
	t.Chdir(workspace)
	start := mustMarshal(t, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "hook_event_name": "SessionStart", "source": "compact"})
	withHookStdin(t, string(start))
	out := captureStdout(t, func() {
		if err := runHookContextMode(dir, false, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("SessionStart(compact) answered into a context CodeBuddy drops: %s", out)
	}

	got := hostsPrompt(t, dir, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt packet lacks %q: %s", want, got)
		}
	}
}
