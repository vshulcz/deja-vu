package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The CLI rejects an unknown --harness (#1113); the MCP side used to return the
// same empty recall as a real-but-empty harness, so a typo in the harness arg
// read to the agent as "no history" instead of "that is not a harness" (#1113).
func TestMCPRecallRejectsUnknownHarness(t *testing.T) {
	seedMCPHarnessStore(t)

	// A typo is refused, and the refusal names the real harnesses.
	_, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":"backpressure","harness":"cluade"}`))
	if err == nil {
		t.Fatal("recall accepted an unknown harness")
	}
	if !strings.Contains(err.Error(), "not a harness") || !strings.Contains(err.Error(), "claude") {
		t.Errorf("refusal does not name the known set: %v", err)
	}

	// A real harness with nothing indexed under it is not an error.
	if _, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":"backpressure","harness":"codex"}`)); err != nil {
		t.Errorf("a valid-but-empty harness errored: %v", err)
	}
	// recall_context and blame guard the same argument.
	if _, err := callMCPTool(index.DefaultDir(), "recall_context", json.RawMessage(`{"query":"x","harness":"cluade"}`)); err == nil {
		t.Error("recall_context accepted an unknown harness")
	}
	if _, err := callMCPTool(index.DefaultDir(), "blame", json.RawMessage(`{"path":"x.go","harness":"cluade"}`)); err == nil {
		t.Error("blame accepted an unknown harness")
	}
}

func seedMCPHarnessStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := root + "/-p"
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"d1","cwd":"/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"a decision about backpressure"}}` + "\n"
	if err := os.WriteFile(proj+"/d1.jsonl", []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = tmp
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}
