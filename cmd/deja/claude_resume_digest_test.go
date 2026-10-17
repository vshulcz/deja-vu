package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code 2.1.300 and later keep the context a SessionStart hook added as
// an attachment in the transcript and replay it on resume, so a resumed session
// got the start digest a second time (#5122). A transcript that does not hold
// one, or holds one only before a compaction, still gets a digest.
func TestClaudeResumeGetsNoSecondDigest(t *testing.T) {
	dir, fork := forkStore(t, false)
	transcript := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p", fork+".jsonl")
	run := func(src string) string {
		payload, err := json.Marshal(map[string]string{"session_id": fork, "source": src, "transcript_path": transcript, "cwd": "/w/p"})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	appendLine := func(v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	digest := run("startup")
	if !strings.Contains(digest, "deja-recall") {
		t.Fatalf("the claude session got no digest at startup, so this proves nothing:\n%s", digest)
	}
	if out := run("resume"); !strings.Contains(out, "deja-recall") {
		t.Fatalf("a resumed transcript that holds no digest got none:\n%s", out)
	}
	// What Claude Code 2.1.302 writes for the startup hook's answer.
	appendLine(map[string]any{
		"type": "attachment", "sessionId": fork, "uuid": "att-1",
		"attachment": map[string]any{
			"type": "hook_additional_context", "hookName": "SessionStart", "hookEvent": "SessionStart",
			"toolUseID": "SessionStart", "content": []string{strings.TrimSpace(digest)},
		},
	})
	if out := run("resume"); strings.TrimSpace(out) != "" {
		t.Errorf("a resumed Claude Code session got its start digest again:\n%s", out)
	}
	// A compaction drops what came before it from the context.
	appendLine(map[string]any{"type": "system", "subtype": "compact_boundary", "sessionId": fork})
	if out := run("resume"); !strings.Contains(out, "deja-recall") {
		t.Errorf("a resumed session whose digest was compacted away got none:\n%s", out)
	}
}
