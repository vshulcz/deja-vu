package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestHookContextHandsClaudeAutoMemoryToNonClaudeAndNotClaude(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeRoot := filepath.Join(tmp, "claude", "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	work := "/tmp/tuip/payments"
	encoded := strings.ReplaceAll(work, "/", "-")
	memDir := filepath.Join(claudeRoot, encoded, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ruleFile := filepath.Join(memDir, "feedback_retry.md")
	ruleContent := "---\n" +
		"name: retry-backoff\n" +
		"description: Retries in payments use internal/retry.Backoff; a hand-rolled sleep loop is rejected in review (zebra-quartz-17)\n" +
		"type: feedback\n" +
		"---\n" +
		"Always use internal/retry.Backoff in payments.\n"
	if err := os.WriteFile(ruleFile, []byte(ruleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	runHook := func(payload string) sessionStartHookResponse {
		t.Helper()
		withHookStdin(t, payload)
		out := captureStdout(t, func() {
			if err := runHookContext(dir, false); err != nil {
				t.Fatalf("runHookContext: %v", err)
			}
		})
		if strings.TrimSpace(out) == "" {
			return sessionStartHookResponse{}
		}
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("unmarshal response: %v\n%s", err, out)
		}
		return resp
	}

	// 1. Exact #5095 repro: non-Claude session gets the Claude auto-memory rule.
	codexResp := runHook(`{"session_id":"probe-c1","cwd":"/tmp/tuip/payments","hook_event_name":"SessionStart","source":"startup"}`)
	if !strings.Contains(codexResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("non-Claude session did not receive zebra-quartz-17:\n%s", codexResp.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(codexResp.HookSpecificOutput.AdditionalContext, "[feedback_retry.md]") {
		t.Fatalf("block did not name the memory file for user visibility/veto:\n%s", codexResp.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(codexResp.SystemMessage, "Claude auto-memory") {
		t.Fatalf("receipt did not announce Claude auto-memory carryover: %q", codexResp.SystemMessage)
	}

	// 2. Claude Code session in the same directory must not get its own auto-memory twice.
	claudeTranscript := filepath.Join(claudeRoot, encoded, "claude-s1.jsonl")
	claudePayload, err := json.Marshal(map[string]string{
		"session_id":      "claude-s1",
		"cwd":             work,
		"transcript_path": claudeTranscript,
		"hook_event_name": "SessionStart",
		"source":          "startup",
	})
	if err != nil {
		t.Fatal(err)
	}
	claudeResp := runHook(string(claudePayload))
	if strings.Contains(claudeResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("Claude session received its own auto-memory twice:\n%s", claudeResp.HookSpecificOutput.AdditionalContext)
	}

	// 3. User veto via policy.json "ignore" suppresses the memory file.
	polFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", polFile)
	if err := os.WriteFile(polFile, []byte(`{"ignore":["*feedback_retry.md*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	vetoedResp := runHook(`{"session_id":"probe-c2","cwd":"/tmp/tuip/payments","hook_event_name":"SessionStart","source":"startup"}`)
	if strings.Contains(vetoedResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("policy.json ignore did not veto feedback_retry.md:\n%s", vetoedResp.HookSpecificOutput.AdditionalContext)
	}
}
