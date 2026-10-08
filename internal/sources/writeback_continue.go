package sources

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Continue keeps a session as sessions/<sessionId>.json, and `cn --fork <id>`
// loads that file by name. The workspace directory comes from Continue's own
// list, sessions.json, while it still names the session; it is left out
// otherwise, which Continue reads as a session from no workspace. The list is
// Continue's and is not written.

func init() {
	registerWriteBack("continue", func() []string { return []string{filepath.Join(ContinueRoot(), "sessions")} }, renderContinue)
}

func renderContinue(s model.Session, turns []model.Message) (string, []byte, error) {
	if filepath.Base(filepath.Dir(s.Path)) != "sessions" || !strings.EqualFold(filepath.Base(s.Path), s.ID+".json") {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no sessions/<id>.json path for this session, which is where `cn --fork` looks"}
	}
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type item struct {
		Message      message `json:"message"`
		ContextItems []any   `json:"contextItems"`
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = "Untitled Session"
	}
	doc := struct {
		SessionID          string `json:"sessionId"`
		Title              string `json:"title"`
		WorkspaceDirectory string `json:"workspaceDirectory,omitempty"`
		History            []item `json:"history"`
	}{SessionID: s.ID, Title: title, WorkspaceDirectory: continueList(filepath.Dir(s.Path))[s.ID].WorkspaceDirectory}
	for _, m := range turns {
		doc.History = append(doc.History, item{Message: message{Role: m.Role, Content: m.Text}, ContextItems: []any{}})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return s.Path, append(data, '\n'), nil
}
