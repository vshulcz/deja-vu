package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEachToolOutputStreamsOnlyToolRecords(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-stream")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var got []Record
	var harness string
	if err := EachToolOutput(dir, func(m SessionMeta, r Record) {
		got = append(got, r)
		harness = m.Harness
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one tool-output record, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "shellcheck") {
		t.Fatalf("wrong record: %q", got[0].Text)
	}
	// The session is what makes the record countable — a caller clustering
	// errors needs to know which session and which harness hit it.
	if harness != "claude" {
		t.Fatalf("harness = %q", harness)
	}
	if got[0].Time.IsZero() {
		t.Fatal("a record with no time cannot be dated on a report")
	}
}

func TestEachToolOutputOnMissingStore(t *testing.T) {
	if err := EachToolOutput(filepath.Join(t.TempDir(), "nope"), func(SessionMeta, Record) {}); err == nil {
		t.Fatal("a missing store should report an error")
	}
}
