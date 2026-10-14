package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ZCode runs `hook-prompt --strict` and discards a reply with a key its schema
// does not know, recall and all. The flag was read only in hook-context, so
// whenever the "you have been here" line was due ZCode lost the recall too.
func TestStrictPromptHookDropsTheReceiptAndKeepsTheRecall(t *testing.T) {
	ask := func(t *testing.T, args ...string) string {
		t.Helper()
		hermeticEnv(t)
		t.Cleanup(func() { strictHookOutput = false })
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-hay", "focus.jsonl"), "focus", []string{
			`{"type":"user","sessionId":"focus","timestamp":"` + old + `","message":{"role":"user","content":"the quetzalcoatl stampede fix: jittered ttl"}}`,
		})
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(t.TempDir(), "tmp", "hay")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		withHookStdin(t, `{"session_id":"z-1","hook_event_name":"UserPromptSubmit","prompt":"quetzalcoatl stampede regression again"}`)
		out, err := captureRun(t, append([]string{"hook-prompt"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	type reply struct {
		SystemMessage      *string `json:"systemMessage"`
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	decode := func(t *testing.T, out string) reply {
		t.Helper()
		var r reply
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return r
	}

	// The control: without the flag the receipt is due and goes out.
	plain := decode(t, ask(t))
	if plain.SystemMessage == nil || !strings.Contains(*plain.SystemMessage, "you have been here") {
		t.Fatalf("the control carried no receipt, so the strict case proves nothing: %+v", plain)
	}

	strict := decode(t, ask(t, "--strict"))
	if strict.SystemMessage != nil {
		t.Errorf("a --strict reply carries systemMessage, which ZCode rejects with the recall: %q", *strict.SystemMessage)
	}
	if !strings.Contains(strict.HookSpecificOutput.AdditionalContext, "quetzalcoatl") {
		t.Errorf("the recall did not survive --strict: %q", strict.HookSpecificOutput.AdditionalContext)
	}
}
