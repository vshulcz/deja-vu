package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func geminiTree(t *testing.T) (root, chats string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats = filepath.Join(root, "tmp", "my-proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, chats
}

func TestParseGeminiOldJSON(t *testing.T) {
	_, chats := geminiTree(t)
	doc := `{"sessionId":"session-old-1","projectHash":"abc","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:35:00.000Z","messages":[
	 {"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"Hello gemneedle"},
	 {"id":"m2","timestamp":"2026-01-16T18:34:02.000Z","type":"gemini","model":"gemini-3-flash","content":"Hi there!"},
	 {"id":"m3","timestamp":"2026-01-16T18:34:03.000Z","type":"info","content":"noise"}]}`
	p := filepath.Join(chats, "session-old-1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "session-old-1" || ss[0].Harness != "gemini" {
		t.Fatalf("bad session: %#v", ss)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (info skipped): %#v", len(ss[0].Messages), ss[0].Messages)
	}
	if ss[0].Messages[1].Role != "assistant" || ss[0].Messages[1].Text != "Hi there!" {
		t.Fatalf("assistant wrong: %#v", ss[0].Messages[1])
	}
	if ss[0].Project != "my-proj" {
		t.Fatalf("project = %q", ss[0].Project)
	}
}

func TestParseGeminiJSONLWithRewind(t *testing.T) {
	_, chats := geminiTree(t)
	lines := `{"sessionId":"sess-new-1","projectHash":"abc","startTime":"2026-07-01T10:00:00.000Z","lastUpdated":"2026-07-01T10:00:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-01T10:00:01.000Z","type":"user","content":[{"text":"first question"}]}
{"id":"a1","timestamp":"2026-07-01T10:00:02.000Z","type":"gemini","content":"wrong answer","model":"gemini-3"}
{"$rewindTo":"a1"}
{"id":"a2","timestamp":"2026-07-01T10:00:05.000Z","type":"gemini","content":"right answer","model":"gemini-3"}
{"$set":{"lastUpdated":"2026-07-01T10:00:06.000Z"}}
`
	p := filepath.Join(chats, "session-2026-07-01T10-00-sess-new-1.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "sess-new-1" {
		t.Fatalf("bad session: %#v", ss)
	}
	m := ss[0].Messages
	if len(m) != 2 {
		t.Fatalf("messages = %d, want 2 (rewind dropped wrong answer): %#v", len(m), m)
	}
	if m[0].Text != "first question" || m[1].Text != "right answer" {
		t.Fatalf("rewind replay wrong: %#v", m)
	}
	if ss[0].Updated.Second() != 6 {
		t.Fatalf("$set lastUpdated not applied: %v", ss[0].Updated)
	}
}

func TestGeminiProjectFromRegistry(t *testing.T) {
	root, chats := geminiTree(t)
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(`{"projects":{"/Users/x/work/cool-app":"my-proj"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := `{"sessionId":"s1","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:34:00.000Z","messages":[{"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"q"}]}`
	p := filepath.Join(chats, "session-s1.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGeminiFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ss=%v err=%v", ss, err)
	}
	if ss[0].Project != "cool-app" {
		t.Fatalf("project = %q, want cool-app", ss[0].Project)
	}
}

func TestGeminiDedupePrefersJSONL(t *testing.T) {
	in := []model.Session{
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.json"},
		{Harness: "gemini", ID: "dup1", Path: "/a/session-dup1.jsonl"},
		{Harness: "gemini", ID: "solo", Path: "/a/session-solo.json"},
	}
	out := dedupeGeminiSessions(in)
	if len(out) != 2 {
		t.Fatalf("deduped to %d, want 2", len(out))
	}
	if out[0].ID != "dup1" || !strings.HasSuffix(out[0].Path, ".jsonl") {
		t.Fatalf("jsonl not preferred: %#v", out[0])
	}
}
