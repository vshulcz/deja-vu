package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSessionHasRoleAcceptsTheDocumentedSpelling(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "asked"},
		{Role: "tool-output", Text: "ERROR"},
	}}
	// `deja help` promises --role tool; "tool-output" is what is stored (#717).
	for _, role := range []string{"tool", "tool-output", "user"} {
		if !sessionHasRole(s, role) {
			t.Errorf("role %q did not match", role)
		}
	}
	for _, role := range []string{"assistant", "files", "nosuch", ""} {
		if sessionHasRole(s, role) {
			t.Errorf("role %q matched a session without it", role)
		}
	}
	// The alias is one-way: a session whose only work record is a file list
	// is not tool output.
	files := model.Session{Messages: []model.Message{{Role: "files", Text: "/repo/a.go"}}}
	if sessionHasRole(files, "tool") {
		t.Error("a files record answered --role tool")
	}
}

func TestLastRoleToolFindsToolOutput(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	withTool := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"ran it"}}` + "\n" +
		`{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-21T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ERROR: boom"}]}}` + "\n"
	talkOnly := `{"type":"user","sessionId":"b1","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"only talk here"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(withTool), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b1.jsonl"), []byte(talkOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "last", "9", "--role", "tool")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a1") {
		t.Errorf("--role tool missed the session with tool output: %q", out)
	}
	if strings.Contains(out, "b1") {
		t.Errorf("--role tool returned a session without tool output: %q", out)
	}
}
