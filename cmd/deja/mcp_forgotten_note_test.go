package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The CLI says a session was forgotten and answers with the note promoted from
// it; the MCP tools handed the agent the same content with the fact removed.
// "Forgotten" is a decision the user made about that session, which is exactly
// the kind of fact recall exists to carry (#1624).
func TestAnAgentAskingForAForgottenSessionIsToldSo(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision: keep the ticker window at 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s15","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s15.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s15"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s15"); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"s15"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ticker window") {
		t.Fatalf("the agent was answered from something else:\n%s", text)
	}
	if !strings.Contains(text, "is forgotten") {
		t.Errorf("the agent was handed the note without being told the session is gone:\n%s", text)
	}

	// The same two cases the CLI keeps quiet about: the note asked for by its
	// own id, and an ordinary query that happens to land on it.
	text, err = callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"deja-note-claude-s15"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "is forgotten") {
		t.Errorf("asking for the note by its own id was answered with a warning:\n%s", text)
	}
	text, err = callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"ticker window"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "is forgotten") {
		t.Errorf("an ordinary query was answered with a warning about a session nobody named:\n%s", text)
	}
}
