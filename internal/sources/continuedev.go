package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Continue (continuedev/continue) runs in VS Code and JetBrains, and its chat,
// agent and plan modes all write the same file:
//
//	${CONTINUE_GLOBAL_DIR:-~/.continue}/sessions/<sessionId>.json
//	${CONTINUE_GLOBAL_DIR:-~/.continue}/sessions/sessions.json   (the list)
//
// The session file carries the turns; the list carries each session's title,
// creation date and workspace directory. Neither the file nor the turns hold a
// timestamp of their own, so the list's dateCreated is the start and the file's
// mtime is the update — the same reading `sessions.json` gives Continue's own
// history view. Shape from core/index.d.ts (Session, ChatHistoryItem,
// ChatMessage) and core/util/paths.ts (#3062).

// ContinueRoot is where Continue keeps its state.
func ContinueRoot() string {
	if p := os.Getenv("DEJA_CONTINUE_ROOT"); p != "" {
		return p
	}
	return EnvPath("CONTINUE_GLOBAL_DIR", filepath.Join(Home(), ".continue"))
}

// ContinueSessionFiles lists the session documents, without the list file.
func ContinueSessionFiles() []string {
	dir := filepath.Join(ContinueRoot(), "sessions")
	return walkFiles(dir, continueSessionFile)
}

func continueSessionFile(p string) bool {
	if !strings.EqualFold(filepath.Ext(p), ".json") {
		return false
	}
	base := filepath.Base(p)
	// sessions.json is the index of the others, not a conversation.
	if base == "sessions.json" {
		return false
	}
	return filepath.Base(filepath.Dir(p)) == "sessions"
}

func LoadContinue() []model.Session { return parseFiles(ContinueSessionFiles(), ParseContinueFile) }

type continueListEntry struct {
	SessionID          string `json:"sessionId"`
	Title              string `json:"title"`
	DateCreated        string `json:"dateCreated"`
	WorkspaceDirectory string `json:"workspaceDirectory"`
}

// continueList reads sessions.json beside a transcript. Its entries carry the
// only date Continue records and the workspace a session ran in, both of which
// the session file itself omits.
func continueList(dir string) map[string]continueListEntry {
	b, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		return nil
	}
	var entries []continueListEntry
	if json.Unmarshal(b, &entries) != nil {
		return nil
	}
	out := make(map[string]continueListEntry, len(entries))
	for _, e := range entries {
		if e.SessionID != "" {
			out[e.SessionID] = e
		}
	}
	return out
}

type continueSession struct {
	SessionID          string              `json:"sessionId"`
	Title              string              `json:"title"`
	WorkspaceDirectory string              `json:"workspaceDirectory"`
	History            []continueHistoryIt `json:"history"`
}

// A history item is a message plus, on the assistant side, the tool calls it
// made. Those are model tools — read_file, edit_file — rather than commands
// anyone ran, so they are not work records here; what a tool printed comes back
// in the assistant turn that follows.
type continueHistoryIt struct {
	Message continueMessage `json:"message"`
}

type continueMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func ParseContinueFile(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc continueSession
	if json.Unmarshal(b, &doc) != nil {
		// One document per session, as in cline and roo: a file that will not
		// parse is a path deja could not read, not a line.
		diagFileError(path, err)
		return nil, nil
	}
	id := doc.SessionID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	s := model.Session{Harness: "continue", ID: id, Path: path, Title: firstLineTrim(doc.Title)}

	entry := continueList(filepath.Dir(path))[doc.SessionID]
	workspace := doc.WorkspaceDirectory
	if workspace == "" {
		workspace = entry.WorkspaceDirectory
	}
	if workspace != "" {
		s.Project = claudeProjectName(pathToProjectKey(workspace))
	} else {
		s.Project = "continue"
	}
	if s.Title == "" {
		s.Title = firstLineTrim(entry.Title)
	}

	base := continueDate(entry.DateCreated)
	if base.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			base = fi.ModTime()
		}
	}
	for i, it := range doc.History {
		role := it.Message.Role
		if role != "user" && role != "assistant" {
			// system prompts are configuration and tool results arrive under
			// the assistant item that called them.
			continue
		}
		// A turn has no time of its own, so they are laid out in order from the
		// session's start — the same thing every whole-document parser here
		// does, and enough for ordering within the session.
		at := base.Add(time.Duration(i) * time.Second)
		if text := continueText(it.Message.Content); text != "" {
			s.Touch(at)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at})
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// continueText reads the two shapes message.content takes: a plain string, or
// the array of parts a message with images or context blocks carries.
func continueText(raw json.RawMessage) string {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return strings.TrimSpace(b.String())
}

// continueDate reads dateCreated, which Continue writes as an ISO string on
// current versions and as epoch milliseconds in a string on older ones.
func continueDate(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	return parseTimeAny(v)
}
