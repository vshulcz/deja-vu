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
	// Deja's own words, not recalled text: the frame the answer goes into
	// tells the reader to treat what it holds as untrusted.
	if i, j := strings.Index(text, "is forgotten"), strings.Index(text, "<deja-recall>"); i > j {
		t.Errorf("the line is inside the untrusted-data frame:\n%s", text)
	}
	if !strings.HasPrefix(text, "deja: ") {
		t.Errorf("the line does not read as deja's own:\n%s", text)
	}

	// The id path is the other writer, reached when the words find nothing —
	// asked directly, since a query naming the session also matches its text.
	if _, id, ok := contextByID(dir, "deja-note-claude-s15"); !ok {
		t.Error("the id path did not resolve the note")
	} else if id.note != "" {
		t.Errorf("asking for the note by its own id carried a warning: %q", id.note)
	}
	if _, id, ok := contextByID(dir, "s15"); !ok {
		t.Error("the id path did not resolve the session")
	} else if !strings.Contains(id.note, "is forgotten") {
		t.Errorf("the id path hands over the note without the fact: %q", id.note)
	}

	// A word that is in every key is not a reader naming a session.
	text, err = callMCPTool(dir, "recall_context", json.RawMessage(`{"query":"claude"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "is forgotten") {
		t.Errorf("the harness name was read as naming the session:\n%s", text)
	}

	// And the resource reader, the door that takes nothing but an id.
	res, code, msg := mcpResourceRead(dir, "deja://session/s15")
	if res == nil {
		t.Fatalf("the resource reader refused: %d %s", code, msg)
	}
	if !strings.Contains(resourceText(t, res), "is forgotten") {
		t.Errorf("the resource reader hands over the note without the fact:\n%s", resourceText(t, res))
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

// selectorNamesSession is what decides it, and the old substring test fired on
// the harness name and on any single character while never firing for the
// sentence an agent actually sends.
func TestOnlyASelectorThatNamesTheSessionFires(t *testing.T) {
	const key = "claude:s15"
	for _, c := range []struct {
		selector string
		want     bool
	}{
		{"s15", true},
		{"claude:s15", true},
		{"what did we decide in s15", true},
		{"s15abc", false},
		{"claude", false},
		{"a", false},
		{":s1", false},
		{"s1", false},
		{"", false},
		{"   ", false},
	} {
		if got := selectorNamesSession(c.selector, key); got != c.want {
			t.Errorf("selectorNamesSession(%q, %q) = %v, want %v", c.selector, key, got, c.want)
		}
	}
}
