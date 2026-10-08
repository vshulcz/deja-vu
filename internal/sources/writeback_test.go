package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func writeBackSample(harness, id, path string) model.Session {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return model.Session{
		Harness: harness, ID: id, Path: path, Started: at,
		Messages: []model.Message{
			{Role: "user", Text: "why does the pool exhaust <under load> & retry", Time: at},
			{Role: "command", Text: "$ go test ./internal/pool", Time: at.Add(time.Second)},
			{Role: RoleToolOutput, Text: "FAIL pool exhausted", Time: at.Add(2 * time.Second)},
			{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
			{Role: "user", Text: "done?"},
		},
	}
}

// writeAndRead renders s, writes it where it says, and reads it back with the
// harness's own parser.
func writeAndRead(t *testing.T, s model.Session, parse func(string) ([]model.Session, error)) []model.Message {
	t.Helper()
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := parse(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("read back %d sessions, %v", len(ss), err)
	}
	if ss[0].ID != s.ID {
		t.Errorf("read back id %q, want %q", ss[0].ID, s.ID)
	}
	return ss[0].Messages
}

func assertTurns(t *testing.T, got []model.Message) {
	t.Helper()
	want := []string{"user:why does the pool exhaust <under load> & retry", "assistant:raise MaxOpenConns to 50", "user:done?"}
	var have []string
	for _, m := range got {
		have = append(have, m.Role+":"+m.Text)
	}
	if strings.Join(have, "|") != strings.Join(want, "|") {
		t.Errorf("turns read back:\n%s\nwant:\n%s", strings.Join(have, "\n"), strings.Join(want, "\n"))
	}
}

func TestWriteBackClaudeReadsBackAsTheSameTurns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	id := "5a1c0de0-1111-4222-8333-444455556666"
	s := writeBackSample("claude", id, filepath.Join(root, "-work-api", id+".jsonl"))
	assertTurns(t, writeAndRead(t, s, ParseClaudeFile))
}

func TestWriteBackCodexReadsBackAsTheSameTurns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("CODEX_HOME", root)
	id := "01a0f6e6-8cd3-72c2-84ff-367e6191e458"
	path := filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T12-00-00-"+id+".jsonl")
	assertTurns(t, writeAndRead(t, writeBackSample("codex", id, path), ParseCodexRollout))

	// A rollout codex compressed goes back as plain JSONL beside it.
	id2 := "01a0f6e6-8cd3-72c2-84ff-367e6191e459"
	zst := filepath.Join(root, "sessions", "2026", "10", "01", "rollout-2026-10-01T12-00-00-"+id2+".jsonl.zst")
	f, err := RenderWriteBack(writeBackSample("codex", id2, zst))
	if err != nil || f.Path != strings.TrimSuffix(zst, ".zst") {
		t.Fatalf("compressed rollout: %q, %v", f.Path, err)
	}
}

func TestWriteBackStaysInTheStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "projects"))
	id := "5a1c0de0-1111-4222-8333-444455556666"
	for _, p := range []string{
		filepath.Join(root, "elsewhere", id+".jsonl"),
		filepath.Join(root, "projects", "..", "x", id+".jsonl"),
		id + ".jsonl",
	} {
		_, err := RenderWriteBack(writeBackSample("claude", id, p))
		var r *WriteBackRefusal
		if !errors.As(err, &r) {
			t.Errorf("%s: wrote outside the store: %v", p, err)
		}
	}
}

func TestWriteBackRefusesWhatItCannotRebuild(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	id := "5a1c0de0-1111-4222-8333-444455556666"
	s := writeBackSample("claude", id, filepath.Join(root, "-w", id+".jsonl"))
	s.Messages = []model.Message{{Role: "command", Text: "$ ls"}}
	if _, err := RenderWriteBack(s); err == nil || !strings.Contains(err.Error(), "no user or assistant turn") {
		t.Errorf("tool records alone: %v", err)
	}
	for _, h := range []string{"opencode", "goose", "hermes", "crush", "kilocode"} {
		_, err := RenderWriteBack(model.Session{Harness: h, ID: "x"})
		if err == nil || !strings.Contains(err.Error(), "database") && !strings.Contains(err.Error(), ".db") {
			t.Errorf("%s: %v, want the database named", h, err)
		}
	}
}
