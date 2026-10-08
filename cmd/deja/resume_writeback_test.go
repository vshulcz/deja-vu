package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// A transcript the agent deleted is written back from the index, in the
// agent's format and at the path it was read from, and then resumes (#4617).
// The index keeps the commands and tool output the file cannot carry, and
// reads only what the agent appends afterwards.
func TestResumeWriteBackRestoresADeletedClaudeSession(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(tmp, "claude", "projects")
	proj := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	id := "5a1c0de0-1111-4222-8333-444455556666"
	path := filepath.Join(proj, id+".jsonl")
	body := `{"type":"user","sessionId":"` + id + `","uuid":"u1","parentUuid":null,"timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"why does the pool exhaust"}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","uuid":"a1","parentUuid":"u1","timestamp":"2026-10-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/pool"}}]}}` + "\n" +
		`{"type":"user","sessionId":"` + id + `","uuid":"u2","parentUuid":"a1","timestamp":"2026-10-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL pool exhausted","is_error":true}]}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","uuid":"a2","parentUuid":"u2","timestamp":"2026-10-01T10:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"raise MaxOpenConns to 50"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	var out bytes.Buffer
	if err := runResume(dir, []string{id}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runResume(dir, []string{id}, &out); err == nil || !strings.Contains(err.Error(), "--write-back") {
		t.Fatalf("a deleted session should point at --write-back: %v", err)
	}

	out.Reset()
	if err := runResume(dir, []string{id, "--write-back"}, &out); err != nil {
		t.Fatalf("write-back: %v", err)
	}
	if !strings.Contains(out.String(), "claude --resume "+id) {
		t.Errorf("after the write-back resume prints %q, want the claude command", out.String())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing written at %s: %v", path, err)
	}
	for _, want := range []string{"why does the pool exhaust", "raise MaxOpenConns to 50", `"sessionId":"` + id + `"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("written transcript lacks %q:\n%s", want, written)
		}
	}
	if strings.Contains(string(written), "FAIL pool exhausted") {
		t.Errorf("tool output went into the file as a turn:\n%s", written)
	}

	// Run again: the file is there, nothing is rewritten.
	before, _ := os.Stat(path)
	out.Reset()
	if err := runResume(dir, []string{id, "--write-back"}, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("a second write-back touched the file")
	}

	// The agent appends a turn; the index keeps the command record it had and
	// adds the new turn.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user","sessionId":"` + id + `","uuid":"u3","parentUuid":"x","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"is the cap raised"}}` + "\n")
	_ = f.Close()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByPrefix(dir, id)
	if err != nil || !ok {
		t.Fatalf("session gone from the index: %v", err)
	}
	var roles []string
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	got := strings.Join(roles, ",")
	if !strings.Contains(got, "command") || !strings.HasSuffix(got, "user") {
		t.Errorf("after the append the index holds %s; want the command record kept and the new turn added", got)
	}
}

func TestWriteBackNeverOverwritesOrLeavesTheStore(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a", "s.jsonl")
	if err := writeBackCreate(root, p, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeBackCreate(root, p, []byte("two\n")); err == nil || !strings.Contains(err.Error(), "does not overwrite") {
		t.Fatalf("an existing transcript was not refused: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one\n" {
		t.Fatalf("existing transcript changed: %q", b)
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if err := writeBackCreate(root, filepath.Join(root, "link", "s.jsonl"), []byte("x\n")); err == nil {
		t.Fatal("wrote through a link out of the store")
	}
	if _, err := os.Stat(filepath.Join(outside, "s.jsonl")); err == nil {
		t.Fatal("a file landed outside the store")
	}
}

func TestWriteBackRefusalSaysWhatIsMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	s := model.Session{Harness: "opencode", ID: "ses_x", Path: filepath.Join(tmp, "work")}
	err := writeBackSession(index.DefaultDir(), s, &bytes.Buffer{})
	// With no database to ask, opencode's session is not known to be gone,
	// and nothing is written.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s = model.Session{Harness: "nosuch", ID: "x", Path: filepath.Join(tmp, "gone.jsonl")}
	err = writeBackSession(index.DefaultDir(), s, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("an unsupported harness: %v", err)
	}
}
