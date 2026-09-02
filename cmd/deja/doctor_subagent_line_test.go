package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The row said "N subagent transcripts skipped (DEJA_INCLUDE_SUBAGENTS=1)",
// which names the variable as the reason they were skipped. It is the remedy:
// a reader who wants them indexed has to set it, and the old line told them it
// was already set.
func TestDoctorSaysHowToIndexSubagentTranscripts(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// The rule is the path, not the isSidechain flag: what deja declines is a
	// file under a subagents/ directory.
	parent := `{"type":"user","sessionId":"11111111-2222-3333-4444-555555555555","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"the parent's own turn"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "11111111-2222-3333-4444-555555555555.jsonl"), []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	side := `{"type":"user","sessionId":"11111111-2222-3333-4444-555555555555","isSidechain":true,"agentId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","cwd":"/w/p","timestamp":"2026-07-22T10:05:00Z","message":{"role":"user","content":"the subagent's own work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "subagents", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"), []byte(side), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	// No skipping: a test that passes by not running is what let the old
	// wording stand.
	if !strings.Contains(out, "subagent transcripts skipped") {
		t.Fatalf("the fixture did not produce the row this test is about:\n%s", out)
	}
	if !strings.Contains(out, "set DEJA_INCLUDE_SUBAGENTS=1 to index them") {
		t.Errorf("the row does not say how to index them:\n%s", out)
	}
	if strings.Contains(out, "skipped (DEJA_INCLUDE_SUBAGENTS=1)") {
		t.Error("the row still reads as if the variable caused the skip")
	}
}
