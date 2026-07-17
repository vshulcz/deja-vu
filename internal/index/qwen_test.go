package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQwenChangedAndAppendedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	path := filepath.Join(root, "qwen", "projects", "-tmp-index", "chats", "session.jsonl")
	line := `{"type":"user","sessionId":"q","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"index text"}]}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := parseChangedFile("qwen", path, FileState{}); err != nil || len(got) != 1 {
		t.Fatalf("changed qwen = %#v, %v", got, err)
	}
	if got, err := parseAppendedFile("qwen", path, FileState{}); err != nil || len(got) != 1 {
		t.Fatalf("appended qwen = %#v, %v", got, err)
	}
}
