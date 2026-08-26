package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What deja prints for a file has to be what deja can find again. A name with
// two spaces came back with one, and restoring the printed path found nothing
// deja had just listed (#2044).
func TestAPrintedPathFindsItsOwnSpan(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_TOOL_PATHS", "1")
	t.Setenv("DEJA_INDEX_EDITS", "1")

	path := "/tmp/app/two  spaces.go"
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "raise the pool size"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": path}},
			map[string]any{"type": "tool_use", "name": "Edit", "input": map[string]any{
				"file_path": path, "old_string": "size = 20", "new_string": "size = 40"}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	blamed, err := captureRun(t, "blame", "two  spaces.go")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: blame found the file at all, or there is no printed path to
	// carry anywhere.
	printed := ""
	for _, line := range strings.Split(blamed, "\n") {
		if f := strings.TrimSpace(line); strings.HasSuffix(f, "spaces.go") {
			printed = f
			break
		}
	}
	if printed == "" {
		t.Fatalf("blame printed no path for the file:\n%s", blamed)
	}

	out, err := captureRun(t, "restore", printed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 replaced spans recorded") {
		t.Errorf("restoring the path blame printed (%q) found nothing:\n%s", printed, out)
	}
}
