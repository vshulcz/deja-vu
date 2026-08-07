package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A rule that hides everything left session start completely silent, which is
// what a project with no history looks like. The count survives the empty
// digest so the line can say a rule decided it.
func TestHookSaysWhenThePolicyWithheldEverything(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage      string `json:"systemMessage"`
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		if strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ticker window") {
			t.Fatalf("a withheld session was injected anyway: %q", resp.HookSpecificOutput.AdditionalContext)
		}
		return resp.SystemMessage
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	got := message(t)
	if !strings.Contains(got, "withheld 1 session") {
		t.Fatalf("session start does not say the rule withheld everything: %q", got)
	}
	if !strings.Contains(got, "nothing activates") {
		t.Fatalf("the line does not name the rule: %q", got)
	}
	// Repeating it every session start would be wallpaper.
	if again := message(t); strings.Contains(again, "withheld") {
		t.Fatalf("the same line was repeated on the next session start: %q", again)
	}
}
