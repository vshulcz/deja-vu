package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The chain the payload rides is pinned in hook_env_untouched_test.go, which
// also holds what replaced the export deja used to write (#2185).

// End to end: the payload alone must be enough for both hooks.
func TestHooksRecallFromPayloadCWD(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	at := time.Now().AddDate(0, 0, -2).UTC().Format(time.RFC3339)
	body := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "a1", "cwd": root, "timestamp": at,
		"message": map[string]any{"role": "user", "content": "the retry budget keeps blowing up under load"},
	}) + claudeRecord(t, map[string]any{
		"type": "assistant", "sessionId": "a1", "cwd": root, "timestamp": at,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "decision: cap retries at three"}}},
	})
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	payload := hookPayload(t, map[string]string{"prompt": "the retry budget keeps blowing up", "cwd": root})
	if err := runHookPromptMode(dir, strings.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retry budget") {
		t.Errorf("hook-prompt recalled nothing from the payload cwd: %q", out.String())
	}
}
