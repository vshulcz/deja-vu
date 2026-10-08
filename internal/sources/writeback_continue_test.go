package sources

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBackContinueReadsBackWithTheListedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	const id = "9bf46774-02b2-4c4b-919d-d668cd46795e"
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	list := `[{"sessionId":"` + id + `","title":"pool","dateCreated":"1791455494024","workspaceDirectory":"/work/api"}]`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	s := writeBackSample("continue", id, filepath.Join(dir, id+".json"))
	assertTurns(t, writeAndRead(t, s, ParseContinueFile))
	b, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		WorkspaceDirectory string `json:"workspaceDirectory"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.WorkspaceDirectory != "/work/api" {
		t.Errorf("workspaceDirectory %q, want the list's", doc.WorkspaceDirectory)
	}
}

func TestWriteBackRooRefuses(t *testing.T) {
	s := model.Session{Harness: "roo", ID: "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a", Path: filepath.Join(t.TempDir(), "api_conversation_history.json"),
		Messages: []model.Message{{Role: "user", Text: "hi"}}}
	_, err := RenderWriteBack(s)
	var r *WriteBackRefusal
	if CanWriteBack("roo") || !errors.As(err, &r) || !strings.Contains(r.Reason, "task history") {
		t.Fatalf("got %v, want a refusal naming Roo's task history", err)
	}
}
