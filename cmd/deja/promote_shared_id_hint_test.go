package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shared-id notice used to advise `--harness`, which show and last accept
// but promote/handoff/resume/share reject with "unknown flag" — so the reader
// promote left picking in silence (#872) was handed advice their command could
// not follow. The notice now names the harness:id form every selector accepts,
// and this test holds both halves: the advice is emitted, and it works.
func TestPromoteSharedIDHintIsFollowable(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "-proj")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "shared99.jsonl"),
		`{"type":"user","sessionId":"shared99","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"claude side content"}}`)
	write(filepath.Join(qwen, "shared99.jsonl"),
		`{"type":"user","sessionId":"shared99","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","parts":[{"text":"qwen side content"}]}}`)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Bare shared id: the notice names both harness:id forms, never the flag
	// promote rejects.
	notice, err := captureRunStderr(t, "promote", "shared99")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "claude:shared99 or qwen:shared99") {
		t.Errorf("shared-id notice did not name the followable form:\n%s", notice)
	}
	if strings.Contains(notice, "--harness") {
		t.Errorf("notice still advised a flag promote rejects:\n%s", notice)
	}

	// The advised form resolves without the "unknown flag" the reader hit when
	// they followed the old --harness advice.
	out, err := captureRunStderr(t, "promote", "qwen:shared99")
	if err != nil {
		t.Fatalf("advised form failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("advised form was rejected:\n%s", out)
	}
}
