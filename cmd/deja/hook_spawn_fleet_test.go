package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every dedupe in this tree is per session, and a spawned agent's hook fires
// with the parent's session id — so a fleet spawned from one parent could all
// count as one reader and start blind. It does not: the key is the parent plus
// the instructions, so ten agents are ten readers and what the key suppresses
// is the same agent spawned twice (#2531).
func TestAFleetOfSpawnedAgentsEachGetMemory(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two, not more: a term living in three sessions of a small store clears
	// neither the idf floor nor the "one or two sessions identify themselves"
	// rescue, and the ranking then matches nothing — which is the fixture
	// mistake 465 measured, not a property of the hook.
	for k := 0; k < 2; k++ {
		id := fmt.Sprintf("kafka%d", k)
		writeClaudeFixture(t, filepath.Join(root, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-0` + fmt.Sprint(k+2) + `T03:04:05Z","message":{"role":"user","content":"the kafka consumer rebalance keeps flapping on the orders topic"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-0` + fmt.Sprint(k+2) + `T03:04:06Z","message":{"role":"assistant","content":"The fix was to raise session.timeout.ms above the poll interval; nothing else held."}}`,
		})
	}
	// Unrelated work, so the words of the question carry weight: in a store
	// where every session says the same thing no term is informative and the
	// ranking matches nothing (the lesson of 430, 437 and 465).
	for i, top := range []string{"the sidebar layout", "the invoice renderer", "the webpack config",
		"the login throttle", "the avatar uploader", "the cron scheduler", "the email templates",
		"the feature flags", "the toast component", "the graphql schema"} {
		id := fmt.Sprintf("t%d", i)
		writeClaudeFixture(t, filepath.Join(root, "app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-01T0` + fmt.Sprint(i%10) + `:00:00Z","message":{"role":"user","content":"work on ` + top + ` today"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-01T0` + fmt.Sprint(i%10) + `:00:01Z","message":{"role":"assistant","content":"changed ` + top + ` and the tests pass"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "src", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	spawn := func(prompt string) string {
		t.Helper()
		payload := `{"hook_event_name":"PreToolUse","tool_name":"Task","tool_input":` +
			`{"prompt":` + mustJSON(t, prompt) + `,"subagent_type":"general-purpose","model":"sonnet"},"session_id":"parent"}`
		out := toolHookRun(t, payload)
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			HookSpecificOutput struct {
				PermissionDecision string                     `json:"permissionDecision"`
				UpdatedInput       map[string]json.RawMessage `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("the spawn reply is not JSON: %q", out)
		}
		if resp.HookSpecificOutput.PermissionDecision != "allow" {
			t.Errorf("the spawn was not allowed: %q", out)
		}
		// A PreToolUse reply replaces the input, so everything the parent chose
		// has to come back with it.
		for _, field := range []string{"subagent_type", "model", "prompt"} {
			if _, ok := resp.HookSpecificOutput.UpdatedInput[field]; !ok {
				t.Errorf("the reply dropped %q from the tool input: %q", field, out)
			}
		}
		var extended string
		_ = json.Unmarshal(resp.HookSpecificOutput.UpdatedInput["prompt"], &extended)
		return extended
	}

	const work = "Investigate why the kafka consumer rebalance keeps flapping on the orders topic."
	first := spawn(work)
	if !strings.Contains(first, "deja-recall") {
		t.Fatalf("the first spawned agent got no memory: %q", first)
	}
	// A second agent on the same work is the repeat the key is meant to catch,
	// and it is still not left blind — it is served what the first was not.
	second := spawn(work)
	if !strings.Contains(second, "deja-recall") {
		t.Errorf("a second agent on the same work started blind: %q", second)
	}
	// Which session each one is handed is the dedupe's business and may change:
	// serving both the same best answer would be a defensible choice. What must
	// not change is that the second agent is spoken to at all.
	if second != first {
		t.Logf("the two agents were handed different sessions, which is what the per-reader key does today")
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
