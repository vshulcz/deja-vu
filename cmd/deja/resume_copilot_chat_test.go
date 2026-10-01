package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// VS Code keeps chat history per workspace and Chat: Show Chats lists only the
// open one's, so resume has to say which folder to open (#4223).
func TestResumeCopilotChatNamesTheFolderToOpen(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "my proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspaceStorage", "abc")
	if err := os.MkdirAll(filepath.Join(ws, "chatSessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(proj)}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"folder":"`+u.String()+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "copilot-chat", ID: "11111111-2222-3333-4444-555555555555", Path: filepath.Join(ws, "chatSessions", "11111111-2222-3333-4444-555555555555.json")}
	_, _, err := resumeCommand(s)
	if err == nil || !strings.Contains(err.Error(), shellQuoteIfNeeded(proj)) {
		t.Fatalf("resume does not name the folder %s: %v", proj, err)
	}

	// The newer extension writes GitHub.copilot-chat/transcripts/<id>.jsonl
	// under the same storage hash.
	s.Path = filepath.Join(ws, "GitHub.copilot-chat", "transcripts", "11111111-2222-3333-4444-555555555555.jsonl")
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), shellQuoteIfNeeded(proj)) {
		t.Fatalf("a transcript-format chat does not name the folder %s: %v", proj, err)
	}

	// An empty-window chat has no folder: the plain line stays.
	s.Path = filepath.Join(root, "globalStorage", "emptyWindowChatSessions", "x.json")
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "Show Chats") || strings.Contains(err.Error(), "code ") {
		t.Fatalf("an empty-window chat got %v", err)
	}
}
