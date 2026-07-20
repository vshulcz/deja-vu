package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCopilotFixture(t *testing.T, root, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func copilotFixtureLines() []string {
	return []string{
		`{"type":"session.start","data":{"sessionId":"7cf44517-55ca-435c-893a-3fde1973a44e","startTime":"2026-07-20T05:13:54.687Z","context":{"cwd":"/Users/x/coding/gateway"}},"timestamp":"2026-07-20T05:13:54.707Z"}`,
		`{"type":"session.model_change","data":{"newModel":"gpt-5-mini"},"timestamp":"2026-07-20T05:13:58.117Z"}`,
		`{"type":"system.message","data":{"role":"system","content":"You are the GitHub Copilot CLI"},"timestamp":"2026-07-20T05:13:58.500Z"}`,
		`{"type":"user.message","data":{"content":"why is the pool exhausted"},"timestamp":"2026-07-20T05:13:58.653Z"}`,
		`{"type":"assistant.turn_start","data":{},"timestamp":"2026-07-20T05:13:58.678Z"}`,
		`{"type":"assistant.message","data":{"content":"the metrics poller leaks rows"},"timestamp":"2026-07-20T05:14:00.973Z"}`,
		`{"type":"session.shutdown","data":{},"timestamp":"2026-07-20T05:14:01.029Z"}`,
	}
}

func TestParseCopilotFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	p := writeCopilotFixture(t, root, "dir-name-not-id", copilotFixtureLines())
	ss, err := ParseCopilotFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse = %v, %v", ss, err)
	}
	s := ss[0]
	if s.Harness != "copilot" || s.ID != "7cf44517-55ca-435c-893a-3fde1973a44e" {
		t.Fatalf("session meta = %#v", s)
	}
	if s.Project != "coding/gateway" {
		t.Fatalf("project = %q", s.Project)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Text != "the metrics poller leaks rows" {
		t.Fatalf("messages = %#v", s.Messages)
	}
	if s.Started.IsZero() || s.Updated.Before(s.Started) {
		t.Fatalf("times = %v %v", s.Started, s.Updated)
	}
}

func TestParseCopilotFileFromOffset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	lines := copilotFixtureLines()
	p := writeCopilotFixture(t, root, "abc", lines)
	head := strings.Join(lines[:6], "\n") + "\n"
	ss, err := ParseCopilotFileFromOffset(p, int64(len(head)))
	if err != nil {
		t.Fatal(err)
	}
	// Only session.shutdown remains past the offset: no messages, so no session.
	if len(ss) != 0 {
		t.Fatalf("offset tail sessions = %#v", ss)
	}
	off := int64(len(strings.Join(lines[:3], "\n") + "\n"))
	ss, err = ParseCopilotFileFromOffset(p, off)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("offset parse = %#v, %v", ss, err)
	}
	// The ID falls back to the directory name when session.start is behind the offset.
	if ss[0].ID != "abc" {
		t.Fatalf("fallback id = %q", ss[0].ID)
	}
}

func TestCopilotDiscoveryAndProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COPILOT_ROOT", root)
	writeCopilotFixture(t, root, "s1", copilotFixtureLines())
	// A stray non-events file must not be discovered.
	if err := os.WriteFile(filepath.Join(root, "s1", "workspace.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := CopilotSessionFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "events.jsonl" {
		t.Fatalf("discovery = %v", files)
	}
	if got := LoadCopilot(); len(got) != 1 || got[0].Harness != "copilot" {
		t.Fatalf("LoadCopilot = %#v", got)
	}
	for in, want := range map[string]string{
		"/Users/x/coding/gateway": "coding/gateway",
		"/gateway":                "gateway",
		"/":                       "",
		"":                        "",
	} {
		if got := copilotProjectName(in); got != want {
			t.Fatalf("copilotProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}
