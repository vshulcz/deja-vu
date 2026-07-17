package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseQwenFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	project := filepath.Join(root, "qwen", "projects", "-tmp-deja-vu", "chats")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"first"},{"text":" question"}]}}
{"type":"assistant","sessionId":"q-1","timestamp":1767337505,"message":{"role":"model","parts":[{"text":"private reasoning","thought":true},{"text":"surface"},{"text":" answer"}]}}
{"type":"system","sessionId":"q-1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"model","parts":[{"text":"skip"}]}}
{"type":"assistant","sessionId":"q-1","timestamp":"2026-01-02T03:07:05Z","message":{"parts":[{"text":"fallback"}]}}
{"type":"assistant",`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseQwenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Project != filepath.Join("deja", "vu") || len(ss[0].Messages) != 3 {
		t.Fatalf("parsed sessions = %#v", ss)
	}
	if ss[0].Messages[0].Text != "first\n question" || ss[0].Messages[1].Text != "surface\n answer" || ss[0].Messages[2].Role != "assistant" {
		t.Fatalf("parsed messages = %#v", ss[0].Messages)
	}
}
