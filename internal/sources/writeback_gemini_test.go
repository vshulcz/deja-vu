package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func writeBackTestTurns() []model.Message {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	return []model.Message{
		{Role: "user", Text: "why does the pool exhaust", Time: at},
		{Role: "command", Text: "$ go test ./pool", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "the cap is 10; raise it to 50", Time: at.Add(2 * time.Second)},
		{Role: "tool-output", Text: "ok", Time: at.Add(3 * time.Second)},
		{Role: "user", Text: "done, <b>thanks</b> & bye", Time: at.Add(4 * time.Second)},
	}
}

func checkWriteBackTurns(t *testing.T, got []model.Message) {
	t.Helper()
	var want []model.Message
	for _, m := range writeBackTestTurns() {
		if m.Role == "user" || m.Role == "assistant" {
			want = append(want, m)
		}
	}
	var conv []model.Message
	for _, m := range got {
		if m.Role == "user" || m.Role == "assistant" {
			conv = append(conv, m)
		}
	}
	if len(conv) != len(want) {
		t.Fatalf("read back %d turns, want %d: %#v", len(conv), len(want), got)
	}
	for i := range want {
		if conv[i].Role != want[i].Role || conv[i].Text != want[i].Text || !conv[i].Time.Equal(want[i].Time) {
			t.Errorf("turn %d = %#v, want %#v", i, conv[i], want[i])
		}
	}
}

func writeBackToDisk(t *testing.T, s model.Session) WriteBackFile {
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
	return f
}

func TestWriteBackGeminiReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	work := filepath.Join(t.TempDir(), "app")
	project := filepath.Join(root, "tmp", "app")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".project_root"), []byte(work), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session-2026-10-01T10-00-0b1c2d3e.jsonl", "session-2026-10-01T10-00-0b1c2d3e.json"} {
		s := model.Session{Harness: "gemini", ID: "0b1c2d3e-1111-4222-8333-444455556666",
			Path: filepath.Join(project, "chats", name), Messages: writeBackTestTurns()}
		f := writeBackToDisk(t, s)
		if f.Turns != 3 || f.Root != filepath.Join(root, "tmp") {
			t.Errorf("%s: turns %d root %s", name, f.Turns, f.Root)
		}
		ss, err := ParseGeminiFile(f.Path)
		if err != nil || len(ss) != 1 || ss[0].ID != s.ID {
			t.Fatalf("%s: read back %#v, %v", name, ss, err)
		}
		checkWriteBackTurns(t, ss[0].Messages)
	}
}

func TestWriteBackGeminiNeedsTheProjectDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GEMINI_ROOT", root)
	s := model.Session{Harness: "gemini", ID: "0b1c2d3e-1111-4222-8333-444455556666",
		Path: filepath.Join(root, "tmp", "app", "chats", "session-2026-10-01T10-00-0b1c2d3e.jsonl"), Messages: writeBackTestTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("wrote a session whose project directory nothing names")
	}
	// An older store keys the folder by the hash itself.
	s.Path = filepath.Join(root, "tmp", "c69b19a0c961b32c9ec990e03b714944df3803e77686cc8068f40f446c48f911", "chats", "session-2026-10-01T10-00-0b1c2d3e.json")
	if _, err := RenderWriteBack(s); err != nil {
		t.Fatalf("hash-named project refused: %v", err)
	}
}
