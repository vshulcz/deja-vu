package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// The compaction block is built from a session's own records, and a record can
// be a path or a command the parser lifted out of a tool call — payloads the
// serving-path guard never covered, because its dirt is always in the message
// text (#2000). This block is injected into a context window that was just
// emptied, and a command row is the string a person is most likely to copy.
func TestCompactEvidenceStripsControlBytes(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	esc := "\x1b[31m"
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// The escape is written as a \u001b escape in the JSON, the way a harness
	// writes it. A raw control byte inside a JSON string is invalid JSON and
	// the line would be dropped before any of this (#1993).
	user := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "why does pgbouncer time out"},
	})
	asst := rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/pool" + esc + ".go"}},
			map[string]any{"type": "tool_use", "name": "Bash",
				"input": map[string]any{"command": "psql -c 'show pool" + esc + "'"}},
			// The same path twice, once with a stray byte on the end. Cleaning
			// the entry without deduping on the cleaned spelling printed it as
			// two identical rows out of a block budgeted at eight. A bare
			// control byte, not an escape sequence: SafeText replaces the ESC
			// and leaves the "[31m" behind, which is a different path and
			// should stay a different row.
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/retry.go\u0007"}},
			map[string]any{"type": "tool_use", "name": "Read",
				"input": map[string]any{"file_path": "/tmp/app/retry.go"}},
		}},
	})
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(user+"\n"+asst+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	got := compactEvidence(os.Getenv("DEJA_INDEX_DIR"), "s1", "/tmp/app")
	// The premise: the block has to hold both kinds of row, or a clean answer
	// here means the records never arrived rather than that they were stripped.
	if !strings.Contains(got, "files it touched:") || !strings.Contains(got, "psql") {
		t.Fatalf("the block is missing the rows this is about:\n%q", got)
	}
	if n := strings.Count(got, "retry.go"); n != 1 {
		t.Errorf("the same path with and without a stray byte is one row, printed %d times:\n%q", n, got)
	}
	for _, r := range got {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			t.Errorf("the compaction block carries %q, which reaches the model's context and the terminal that renders it:\n%q", r, got)
			break
		}
	}
}
