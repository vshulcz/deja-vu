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

// The harness names the project in its payload; deja read only the
// environment, so a host that sends `cwd` without exporting
// CLAUDE_PROJECT_DIR recalled nothing — indistinguishable from having no
// memory (#759).
func TestAdoptHookCWD(t *testing.T) {
	t.Run("takes the payload when the environment is silent", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		adoptHookCWD("/w/from-payload")
		if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "/w/from-payload" {
			t.Errorf("CLAUDE_PROJECT_DIR = %q", got)
		}
	})
	t.Run("never overrides an explicit environment", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", "/w/from-env")
		adoptHookCWD("/w/from-payload")
		if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "/w/from-env" {
			t.Errorf("payload overrode the environment: %q", got)
		}
	})
	t.Run("an empty payload changes nothing", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		adoptHookCWD("")
		if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "" {
			t.Errorf("CLAUDE_PROJECT_DIR = %q", got)
		}
	})
}

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
	body := `{"type":"user","sessionId":"a1","cwd":"` + filepath.ToSlash(root) + `","timestamp":"` + at + `","message":{"role":"user","content":"the retry budget keeps blowing up under load"}}` + "\n" +
		`{"type":"assistant","sessionId":"a1","cwd":"` + filepath.ToSlash(root) + `","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":"decision: cap retries at three"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	payload := `{"prompt":"the retry budget keeps blowing up","cwd":"` + filepath.ToSlash(root) + `"}`
	if err := runHookPromptMode(dir, strings.NewReader(payload), &out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retry budget") {
		t.Errorf("hook-prompt recalled nothing from the payload cwd: %q", out.String())
	}
}
