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

func TestCopilotWriteBackReadsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	id := "315781b9-c57e-4cb7-9f26-a5db1d7f93d7"
	s := model.Session{
		Harness: "copilot", ID: id,
		Path:    filepath.Join(home, "session-state", id, "events.jsonl"),
		Started: at,
		Messages: []model.Message{
			{Role: "user", Text: "why is the queue stuck", Time: at},
			{Role: RoleToolOutput, Text: "3 jobs waiting", Time: at.Add(time.Second)},
			{Role: "assistant", Text: "the worker is paused", Time: at.Add(2 * time.Second)},
		},
	}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Data), `"copilotVersion"`) {
		t.Error("session.start without copilotVersion, which Copilot reads as corrupted")
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotFile(f.Path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse back: %v %d", err, len(ss))
	}
	var got []string
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+":"+m.Text)
	}
	if want := "user:why is the queue stuck|assistant:the worker is paused"; strings.Join(got, "|") != want {
		t.Errorf("turns read back = %q, want %q", strings.Join(got, "|"), want)
	}
	if ss[0].ID != id {
		t.Errorf("id = %q", ss[0].ID)
	}
}

func TestCopilotWriteBackRefusesAPathOutsideTheStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	t.Setenv("DEJA_COPILOT_ROOT", "")
	id := "315781b9-c57e-4cb7-9f26-a5db1d7f93d7"
	s := model.Session{Harness: "copilot", ID: id, Path: filepath.Join(t.TempDir(), id, "events.jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}
