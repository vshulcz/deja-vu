package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A Kimi or Qwen sub-agent log is a session in deja under
// DEJA_INCLUDE_SUBAGENTS=1, but neither client opens one on its own: resume
// printed `kimi --session agent-1-<parent>`, an id Kimi has never heard of
// (#4483). It says so, and names the session that spawned it.
func TestResumeRefusesAKimiOrQwenSubagent(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	qwenParent := qwenTranscriptIn(t, filepath.Join(tmp, "qwen"), proj, "q-1", true)
	qwenChild := filepath.Join(filepath.Dir(filepath.Dir(qwenParent)), "subagents", "q-1", "agent-a1.jsonl")
	for _, s := range []model.Session{
		{Harness: "qwen", ID: "agent-a1-q-1", Kind: "subagent", Parent: "q-1", Path: qwenChild},
		{Harness: "kimi", ID: "agent-1-session_t01", Kind: "subagent", Parent: "session_t01",
			Path: filepath.Join(tmp, "kimi", "sessions", "wd", "session_t01", "agents", "agent-1", "wire.jsonl")},
		{Harness: "gemini", ID: "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", Kind: "subagent", Parent: "f7b7a064-d884-4582-a228-e95a8c1ac4f3",
			Path: filepath.Join(tmp, "gemini", "tmp", "proj", "chats", "f7b7a064-d884-4582-a228-e95a8c1ac4f3", "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl")},
	} {
		dir, cmd, err := resumeCommand(s)
		if err == nil {
			t.Errorf("%s: printed %q for a sub-agent run", s.Harness, resumeCmdLine(dir, cmd))
			continue
		}
		for _, want := range []string{"sub-agent", "deja resume " + s.Parent} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not say %q", s.Harness, err, want)
			}
		}
	}
	// Control: the parent still resumes.
	if _, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "q-1", Path: qwenParent}); err != nil || cmd != "qwen -r q-1" {
		t.Errorf("the parent resumes as %q: %v", cmd, err)
	}
}

// A Kimi /btw side question is read as a fork of the session it was asked in,
// and Kimi does not open the fork on its own either (#4484).
func TestResumeRefusesAKimiBtwFork(t *testing.T) {
	s := model.Session{Harness: "kimi", ID: "agent-1-session_t01", Kind: "fork", Parent: "session_t01",
		Path: filepath.Join(t.TempDir(), "kimi", "sessions", "wd", "session_t01", "agents", "agent-1", "wire.jsonl")}
	dir, cmd, err := resumeCommand(s)
	if err == nil {
		t.Fatalf("printed %q for a /btw fork", resumeCmdLine(dir, cmd))
	}
	if !strings.Contains(err.Error(), "deja resume session_t01") {
		t.Errorf("refusal %q does not name the session to resume", err)
	}
}

// A session /fork or `kimi fork` made is a session of its own, which
// `kimi --session` opens; resume called it a /btw side question (#5226).
func TestResumeOpensAKimiSessionFork(t *testing.T) {
	s := model.Session{Harness: "kimi", ID: "session_fork", Kind: "fork", Parent: "session_src",
		Path: filepath.Join(t.TempDir(), "kimi", "sessions", "wd", "session_fork", "agents", "main", "wire.jsonl")}
	_, cmd, err := resumeCommand(s)
	if err != nil || !strings.Contains(cmd, "session_fork") {
		t.Fatalf("a kimi session fork resumes as %q: %v", cmd, err)
	}
}

// A Cursor CLI subagent was told it had left cursor-agent's store, which it
// was never in: its state lives in the parent's chat, so the refusal names
// the parent. Hermes and OpenClaw do reopen a child, checked live, so theirs
// is passed through (#5303).
func TestResumeSubagentChildPerClient(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	cwd := filepath.Join(tmp, "work", "my-app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := "11111111-0000-0000-0000-000000000001"
	child := "22222222-0000-0000-0000-000000000002"
	tp := seedCursorChat(t, cli, cwd, parent)
	cp := filepath.Join(filepath.Dir(tp), "subagents", child+".jsonl")
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"write the notes"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: child, Kind: "subagent", Parent: parent, Path: cp})
	if err == nil {
		t.Fatalf("cursor: printed %q for a subagent", resumeCmdLine(dir, cmd))
	}
	for _, want := range []string{"sub-agent", "deja resume " + parent} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cursor: refusal %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "no longer in") {
		t.Errorf("cursor: refusal %q says the child left a store it was never in", err)
	}
	// Control: the parent still resumes.
	if _, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: parent, Path: tp}); err != nil || !strings.Contains(cmd, "cursor-agent --resume "+parent) {
		t.Errorf("cursor parent resumes as %q: %v", cmd, err)
	}

	hermes := model.Session{Harness: "hermes", ID: "20261018_084719_441ac7", Kind: "subagent", Parent: "20261018_084704_a4b1b7",
		Path: filepath.Join(tmp, "hermes", "state.db")}
	if _, cmd, err := resumeCommand(hermes); err != nil || cmd != "hermes --resume "+hermes.ID {
		t.Errorf("hermes child resumes as %q: %v", cmd, err)
	}

	sessions := filepath.Join(tmp, "openclaw", "agents", "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	key := "agent:main:subagent:8b21e74e-29b0-4184-ab79-488c87723f7b"
	if err := os.WriteFile(filepath.Join(sessions, "sessions.json"), []byte(`{"agent:main:main":{"sessionId":"p"},"`+key+`":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claw := model.Session{Harness: "openclaw", ID: "kid", Kind: "subagent", Parent: "p", Path: filepath.Join(sessions, "kid.jsonl")}
	if _, cmd, err := resumeCommand(claw); err != nil || cmd != "openclaw chat --session "+key {
		t.Errorf("openclaw child resumes as %q: %v", cmd, err)
	}
}
