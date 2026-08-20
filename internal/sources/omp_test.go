package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOmpFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-Code-pleasure-course")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "2026-08-17T10-56-36-424Z_01a00f5d-bc48-7000-b56f-33a99ffec433.jsonl")
	data := `{"type":"session","version":3,"id":"01a00f5d-bc48-7000-b56f-33a99ffec433","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/Users/halo/Code/pleasure-course","title":"Download YouTube videos"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-08-17T10:56:36.516Z","model":"ollama-cloud/deepseek-v4-pro:0813"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":[{"type":"text","text":"download the videos"}],"timestamp":1786964225859}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-17T10:57:12.306Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"read the file first"},{"type":"toolCall","id":"ollama:1:read","name":"read","arguments":{"path":"anastasia.md"}},{"type":"text","text":"reading the list"}],"timestamp":1786964232306}}
{"type":"message","id":"t1","parentId":"a1","timestamp":"2026-08-17T10:57:12.500Z","message":{"role":"toolResult","toolCallId":"ollama:1:read","toolName":"read","content":[{"type":"text","text":"some file output"}]}}
{"type":"message","id":"a2","parentId":"t1","timestamp":"2026-08-17T10:57:20.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"timestamp":1786964240000}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	s := ss[0]
	if s.Harness != "omp" {
		t.Fatalf("harness = %q, want omp", s.Harness)
	}
	if s.ID != "01a00f5d-bc48-7000-b56f-33a99ffec433" {
		t.Fatalf("id = %q", s.ID)
	}
	// The header cwd is promoted to the project key, not the lossy directory
	// name (-Code-pleasure-course would decode to pleasure/course).
	if s.Project != claudeProjectName(pathToProjectKey("/Users/halo/Code/pleasure-course")) {
		t.Fatalf("project = %q, want %q", s.Project, claudeProjectName(pathToProjectKey("/Users/halo/Code/pleasure-course")))
	}
	// user + assistant(text only, thinking/toolCall skipped) + tool output + assistant
	if len(s.Messages) != 4 {
		t.Fatalf("want 4 messages, got %d: %#v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "download the videos" {
		t.Fatalf("message[0] = %#v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "reading the list" {
		t.Fatalf("message[1] = %#v (thinking/toolCall must be skipped)", s.Messages[1])
	}
	if s.Messages[2].Role != RoleToolOutput || s.Messages[2].Text != "some file output" {
		t.Fatalf("message[2] = %#v, want tool output", s.Messages[2])
	}
	if s.Messages[3].Role != "assistant" || s.Messages[3].Text != "done" {
		t.Fatalf("message[3] = %#v", s.Messages[3])
	}
}

func TestParseOmpFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-tmp")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line1 := `{"type":"session","version":3,"id":"off-1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/tmp/demo"}` + "\n"
	line2 := `{"type":"message","id":"u1","parentId":null,"timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n"
	line3 := `{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-17T10:57:12.306Z","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}` + "\n"
	path := filepath.Join(project, "session.jsonl")
	if err := os.WriteFile(path, []byte(line1+line2+line3), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := int64(len(line1) + len(line2))
	ss, err := ParseOmpFileFromOffset(path, offset)
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

func TestParseOmpFileEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OMP_ROOT", filepath.Join(root, "omp-sessions"))
	project := filepath.Join(root, "omp-sessions", "-tmp-empty")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "session.jsonl")
	data := `{"type":"session","version":3,"id":"empty-1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/tmp/empty"}
{"type":"model_change","id":"m1","timestamp":"2026-08-17T10:56:36.516Z"}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOmpFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Fatalf("expected no sessions from metadata-only file, got %d", len(ss))
	}
}

func TestOmpDiscoveryAndProjectBase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_OMP_ROOT", "")
	if got := OmpRoot(); got != filepath.Join(root, ".omp", "agent", "sessions") {
		t.Fatalf("default OmpRoot = %q", got)
	}
	ompRoot := filepath.Join(root, "omp-sessions")
	t.Setenv("DEJA_OMP_ROOT", ompRoot)
	proj := filepath.Join(ompRoot, "-Code-adaptivefrontier-legacy")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"s1","timestamp":"2026-08-17T10:56:36.424Z","cwd":"/Users/halo/Code/adaptivefrontier/legacy"}
{"type":"message","timestamp":"2026-08-17T10:57:05.912Z","message":{"role":"user","content":"omp discovery fact"}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := OmpSessionFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("OmpSessionFiles = %v", files)
	}
	ss := LoadOmp()
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Harness != "omp" {
		t.Fatalf("LoadOmp = %#v", ss)
	}
	// Header cwd wins over the encoded directory name.
	if !strings.HasSuffix(ss[0].Project, "legacy") {
		t.Fatalf("omp project = %q, want …legacy", ss[0].Project)
	}
}
