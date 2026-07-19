package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePiFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-deja-vu--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "2026-01-02T03-04-05-000Z_abc-123.jsonl")
	data := `{"type":"session","version":3,"id":"abc-123","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/deja-vu"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-01-02T03:04:06Z","provider":"anthropic","modelId":"claude-sonnet-4"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"what is pi?"}],"timestamp":1767337450000}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think..."},{"type":"text","text":"pi is a coding agent"}],"api":"anthropic","model":"claude-sonnet-4","usage":{"input":10,"output":20},"stopReason":"endTurn","timestamp":1767337455000}}
{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-01-02T03:04:16Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"some output"}]}}
{"type":"message","id":"a2","parentId":"t1","timestamp":"2026-01-02T03:04:20Z","message":{"role":"assistant","content":[{"type":"text","text":"here is the result"}],"timestamp":1767337460000}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	s := ss[0]
	if s.Harness != "pi" {
		t.Fatalf("harness = %q, want pi", s.Harness)
	}
	if s.ID != "abc-123" {
		t.Fatalf("id = %q, want abc-123", s.ID)
	}
	if s.Project != filepath.Join("deja", "vu") {
		t.Fatalf("project = %q, want deja/vu", s.Project)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("want 3 messages (user + 2 assistant), got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "what is pi?" {
		t.Fatalf("message[0] = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "pi is a coding agent" {
		t.Fatalf("message[1] = %#v", s.Messages[1])
	}
	if s.Messages[2].Role != "assistant" || s.Messages[2].Text != "here is the result" {
		t.Fatalf("message[2] = %#v", s.Messages[2])
	}
}

func TestParsePiFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-demo--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"session","version":3,"id":"off-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/demo"}` + "\n"
	line2 := `{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-02T03:04:10Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	line3 := `{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-01-02T03:04:15Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "session.jsonl")
	if err := os.WriteFile(path, []byte(line1+line2+line3), 0o644); err != nil {
		t.Fatal(err)
	}
	// Parse from offset past the header + first message
	offset := int64(len(line1) + len(line2))
	ss, err := ParsePiFileFromOffset(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("want 1 session with 1 message, got %#v", ss)
	}
	if ss[0].Messages[0].Text != "reply" {
		t.Fatalf("message text = %q, want reply", ss[0].Messages[0].Text)
	}
}

func TestParsePiFileEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_PI_ROOT", filepath.Join(root, "pi-sessions"))
	project := filepath.Join(root, "pi-sessions", "--tmp-empty--")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"session","version":3,"id":"empty-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/empty"}
{"type":"model_change","id":"m1","timestamp":"2026-01-02T03:04:06Z"}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParsePiFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("expected no sessions from metadata-only file, got %d", len(ss))
	}
}
