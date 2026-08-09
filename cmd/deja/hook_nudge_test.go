package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The one state nobody ever sets is "rejected" — zero sessions carry it on a
// real store — because it is asked for after the fact. The moment it can be
// captured is the sentence where the user says it failed, so that is where the
// hook asks, whether or not the same prompt earns a recall.
func TestPromptHookAsksToRecordADeadEndWithNoRecallToCarryIt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	// A prompt with no informative terms: the recall path gives up early, and
	// the nudge still has to reach the agent.
	out := promptHookOut(t, dir, "ok we rolled back that change")
	if out == "" {
		t.Fatal("a reported dead end produced nothing")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("not the hook shape: %v (%q)", err, out)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "deja remember") {
		t.Errorf("the nudge does not say how to record it: %q", resp.HookSpecificOutput.AdditionalContext)
	}

	// Twice in a row is noise: a prompt hook is paid on every message.
	if again := promptHookOut(t, dir, "and we reverted the other one too"); again != "" {
		t.Errorf("the nudge repeated within the gap: %q", again)
	}
}

func TestPromptHookStaysSilentOnAnOrdinaryMessage(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if out := promptHookOut(t, dir, "please add a retry to the webhook sender"); out != "" {
		t.Errorf("an ordinary message was answered: %q", out)
	}
	// A question about reverting is the moment before the decision, not the
	// decision, and must not be treated as a report.
	if out := promptHookOut(t, dir, "should we have reverted that?"); out != "" {
		t.Errorf("a question was read as a report: %q", out)
	}
}

func promptHookOut(t *testing.T, dir, prompt string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          prompt,
		"session_id":      "now",
	})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := runHookPrompt(dir, strings.NewReader(string(payload)), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
