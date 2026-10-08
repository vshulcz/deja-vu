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

func TestWriteBackCodeBuddyReadsBack(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	t.Setenv("DEJA_CODEBUDDY_ROOTS", "")
	cwd := t.TempDir()
	folder := filepath.Join(cfg, "projects", strings.TrimPrefix(claudeEncodePath(cwd), "-"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// A sibling transcript names the directory the folder is for.
	sibling := `{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}],"cwd":` + cbJSONString(cwd) + `}` + "\n"
	if err := os.WriteFile(filepath.Join(folder, "other.jsonl"), []byte(sibling), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "01a11b04-6287-726a-a9da-5ed51724ccbf"
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "codebuddy", ID: id, Path: filepath.Join(folder, id+".jsonl"), Messages: []model.Message{
		{Role: "user", Text: "the cache evicts too early", Time: at},
		{Role: "command", Text: "$ go test ./cache", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "raise the TTL", Time: at.Add(2 * time.Second)},
	}}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != s.Path || f.Turns != 2 {
		t.Fatalf("path %q turns %d", f.Path, f.Turns)
	}
	if err := os.WriteFile(f.Path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	back, err := ParseCodeBuddyFile(f.Path)
	if err != nil || len(back) != 1 {
		t.Fatalf("parse: %v %d", err, len(back))
	}
	var got []string
	for _, m := range back[0].Messages {
		if m.Role == "user" || m.Role == "assistant" {
			got = append(got, m.Role+":"+m.Text)
		}
	}
	want := []string{"user:the cache evicts too early", "assistant:raise the TTL"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("read back %q, want %q", got, want)
	}
	if back[0].ID != id {
		t.Fatalf("id %q", back[0].ID)
	}
	if dir := CodeBuddySessionDir(f.Path); dir != cwd {
		t.Fatalf("session dir %q, want %q", dir, cwd)
	}
}

func TestWriteBackCodeBuddyNeedsTheDirectory(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	t.Setenv("DEJA_CODEBUDDY_ROOTS", "")
	id := "01a11b04-0000-726a-a9da-5ed51724ccbf"
	s := model.Session{Harness: "codebuddy", ID: id,
		Path:     filepath.Join(cfg, "projects", "gone-nowhere-xyz", id+".jsonl"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "directory the session ran in") {
		t.Fatalf("want a refusal naming the directory, got %v", err)
	}
}

func cbJSONString(s string) string {
	b, _ := jsonLines([]any{s})
	return strings.TrimSpace(string(b))
}
