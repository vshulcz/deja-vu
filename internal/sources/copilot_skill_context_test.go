package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copilot CLI injects a skill's body as a user.message wrapped in
// <skill-context>; the reader indexed the whole skill as the person's words
// (#3305). Shape from a real 1.0.79 store.
func TestCopilotSkillContextIsNotThePersons(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	dir := filepath.Join(root, "a553c093-4f69-48ce-8e82-669ece70637d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"type":"session.start","data":{"sessionId":"a553c093-4f69-48ce-8e82-669ece70637d"},"timestamp":"2026-09-08T10:00:00.000Z"}`,
		`{"type":"user.message","data":{"content":"<skill-context name=\"deja-history\">\nBase directory for this skill: /Users/me/.copilot/skills/deja-history\n\ndeja does not index Copilot history. It is a consumer: use the deja MCP tools to search memory from the other harnesses.\n</skill-context>"},"timestamp":"2026-09-08T10:00:01.000Z"}`,
		`{"type":"user.message","data":{"content":"<skill-context name=\"deja-history\">\nBase directory for this skill: /Users/me/.copilot/skills/deja-history\n</skill-context>\n\nwhat did we settle about the checkout worker"},"timestamp":"2026-09-08T10:00:02.000Z"}`,
		`{"type":"assistant.message","data":{"content":"It keeps one connection per worker."},"timestamp":"2026-09-08T10:00:05.000Z"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "what did we settle about the checkout worker" {
		t.Errorf("user turns = %q, want the typed question alone", users)
	}
}
