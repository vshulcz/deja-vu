package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Gemini CLI 0.64 fires SessionStart on --resume before it goes back to the
// resumed file, so what the hook answers is written to a new chat file with no
// session header, the transcript_path in the payload. The next start's session
// cleanup takes that file for a corrupt one and deletes every file of the id,
// the real transcript with it (#5133). The digest waits for the first prompt,
// which reaches the model, and nothing is printed at the start.
func TestGeminiResumeStartWaitsForTheFirstPrompt(t *testing.T) {
	tmp := hermeticEnv(t)
	hostsNoWarmup(t)
	home := os.Getenv("HOME")
	cwd := filepath.Join(tmp, "proj")
	claude := filepath.Join(tmp, "claude", "proj")
	for _, d := range []string{cwd, claude} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s0","timestamp":%q,"cwd":%q,"message":{"role":"user","content":"the retry_loop in fetcher drops the last attempt"}}`, at, cwd)
	if err := os.WriteFile(filepath.Join(claude, "s0.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("GEMINI_PROJECT_DIR", cwd)
	t.Setenv("GEMINI_SESSION_ID", "")

	run := func(name, payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() { _ = runHookDeferred(dir, name, nil, commands[name]) })
	}
	payload := func(fields map[string]any) string {
		fields["cwd"] = cwd
		b, _ := json.Marshal(fields)
		return string(b)
	}
	chat := func(id string) string {
		return filepath.Join(home, ".gemini", "tmp", "proj", "chats", "session-2026-10-17T21-46-"+id[:8]+".jsonl")
	}
	start := func(id, source string) string {
		return run("hook-context", payload(map[string]any{"hook_event_name": "SessionStart", "session_id": id, "transcript_path": chat(id), "source": source}))
	}
	prompt := func(id string) string {
		return run("hook-prompt", payload(map[string]any{"hook_event_name": "BeforeAgent", "session_id": id, "transcript_path": chat(id), "prompt": "carry on"}))
	}

	// Control: a fresh start is answered in place.
	if out := start("aaaaaaaa-0000-4000-8000-000000000001", "startup"); !strings.Contains(out, "retry_loop") {
		t.Fatalf("a startup got no digest, so this measures nothing: %q", out)
	}

	resumed := "bbbbbbbb-0000-4000-8000-000000000002"
	if out := start(resumed, "resume"); strings.TrimSpace(out) != "" {
		t.Fatalf("a Gemini resume start was answered; Gemini writes that into a headerless file: %q", out)
	}
	if other := prompt("cccccccc-0000-4000-8000-000000000003"); strings.Contains(other, "retry_loop") {
		t.Fatalf("another session's prompt got the resumed session's digest: %q", other)
	}
	got := prompt(resumed)
	if !strings.Contains(got, "additionalContext") || !strings.Contains(got, "retry_loop") {
		t.Fatalf("the resumed session's first prompt did not carry its digest: %q", got)
	}
	if again := prompt(resumed); strings.Contains(again, "recent history") {
		t.Fatalf("the digest went out twice: %q", again)
	}

	// Qwen Code exports GEMINI_PROJECT_DIR too; its resume is not Gemini's.
	qwen := run("hook-context", payload(map[string]any{"hook_event_name": "SessionStart", "session_id": "dddddddd-0000-4000-8000-000000000004",
		"transcript_path": filepath.Join(home, ".qwen", "projects", "proj", "chats", "d.jsonl"), "source": "resume"}))
	if !strings.Contains(qwen, "retry_loop") {
		t.Fatalf("a non-Gemini resume lost its digest: %q", qwen)
	}
}
