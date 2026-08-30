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
	for _, c := range []struct {
		key, selector string
		resolved      bool
		want          bool
	}{
		{"claude:s15", "s15", false, true},
		{"claude:s15", "claude:s15", false, true},
		{"claude:s15", "what did we decide in s15", false, true},
		{"claude:s15", "s15abc", false, false},
		{"claude:s15", "claude", false, false},
		{"claude:s15", "a", false, false},
		{"claude:s15", ":s1", false, false},
		{"claude:s15", "", false, false},
		{"claude:s15", "   ", false, false},
		// A prefix counts where a resolver answered with this session and
		// nothing else — `deja ctx s1` — and nowhere else.
		{"claude:s15", "s1", true, true},
		{"claude:s15", "s1", false, false},
		// The ids real harnesses write are why: a search query would
		// otherwise name a session by starting with a year or a harness name.
		{"codex:2026-07-31T00-00-00-abc", "what did we ship in 2026", false, false},
		{"cline:cline-task-1767225600000", "cline", false, false},
		{"cline:cline-task-1767225600000", "cline-task-1767225600000", false, true},
		// A short id is still named by naming it, resolver or not.
		{"gemini:s1", "s1", false, true},
	} {
		if got := selectorNamesSession(c.selector, c.key, c.resolved); got != c.want {
			t.Errorf("selectorNamesSession(%q, %q, resolved=%v) = %v, want %v",
				c.selector, c.key, c.resolved, got, c.want)
		}
	}
}

// The line is deja's own and goes above the frame, so its room has to come out
// of the budget before the digest is trimmed — added afterwards it put the
// reply over the size the tool documents, which is what #1797 fixed for the
// frame itself.
func TestTheForgottenLineIsInsideTheBudget(t *testing.T) {
	dir := seedContextDigestSession(t, "proj", "wobble")
	if _, err := captureRunStderr(t, "promote", "wobble", "--note", strings.Repeat("a long note about the shard budget ", 300)); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "wobble"); err != nil {
		t.Fatal(err)
	}
	framed, err := callMCPTool(dir, "recall_context", []byte(`{"query":"wobble"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(framed, "is forgotten") {
		t.Fatalf("the reply does not carry the line this test is about:\n%s", framed[:200])
	}
	if len(framed) > contextMCPBudget {
		t.Errorf("recall_context returned %d bytes, budget is %d", len(framed), contextMCPBudget)
	}
}
