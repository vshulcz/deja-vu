package sources

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The Copilot Chat extension writes a second store, and it is the one with the
// history in it: `workspaceStorage/<ws>/GitHub.copilot-chat/transcripts/<id>.jsonl`.
//
// On the machine in #3637 — VS Code Server 1.137 — there is no `chatSessions`
// directory at all, and 47 transcripts hold eleven weeks of chats; the reader
// found zero files. On this machine, desktop 1.136.1, both layouts sit side by
// side: 28 `chatSessions` directories that deja reads and five transcripts it
// did not. So this is not one version's quirk and not the Server layout's: a
// chat written by the newer extension goes here.
//
// The records are a `type`-discriminated event log rather than the `kind`
// 0/1/2 delta log beside it, which is why this is its own reader rather than a
// branch inside the replay:
//
//	{"type":"session.start","data":{"sessionId":…,"copilotVersion":"0.62.0"},"timestamp":…}
//	{"type":"user.message","data":{"content":"…"},…}
//	{"type":"assistant.message","data":{"content":"…","toolRequests":[{"name":"read_file","arguments":"{\"filePath\":\"…\"}"}]},…}
//	{"type":"tool.execution_start","data":{"toolName":"run_in_terminal","arguments":{…}},…}
//	{"type":"tool.execution_complete","data":{"toolCallId":…,"success":true},…}
//
// A transcript with no `user.message` at all is indexed rather than skipped:
// 11 of the reporter's 47 and all five here are agent runs, and what the agent
// said and the files it touched are the history of that work. Nothing else
// records them — the delta log is a different session store, not a copy.
const (
	copilotAgentDirName   = "transcripts"
	copilotAgentParentDir = "GitHub.copilot-chat"
)

// copilotAgentTranscript reports whether a path is one of those transcripts.
//
// Both names are checked. `transcripts` on its own is a directory name this
// repository already reads for another harness (Claude Code keeps one), and a
// reader that matched on it alone would take those too — the trap #3637 names.
func copilotAgentTranscript(p string) bool {
	if !strings.EqualFold(filepath.Ext(p), ".jsonl") {
		return false
	}
	dir := filepath.Dir(p)
	if filepath.Base(dir) != copilotAgentDirName {
		return false
	}
	return filepath.Base(filepath.Dir(dir)) == copilotAgentParentDir
}

// copilotAgentEvent is one line of the log. `data` is read per type.
type copilotAgentEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// parseCopilotAgentTranscript reads one transcript into a session.
func parseCopilotAgentTranscript(path string, data []byte) ([]model.Session, error) {
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	s := model.Session{Harness: "copilot-chat", ID: id, Path: path}
	var (
		files []string
		seen  = map[string]bool{}
	)
	addPath := func(p string) {
		if p == "" || seen[p] || !IndexToolPaths() {
			return
		}
		seen[p] = true
		files = append(files, p)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var e copilotAgentEvent
		if json.Unmarshal([]byte(raw), &e) != nil {
			// A live session's last line is half-written: the file grows while
			// the chat runs, so one unreadable line at the end is the ordinary
			// state rather than a broken store (#3637).
			diagMalformedLine(path)
			continue
		}
		at := parseTimeAny(e.Timestamp)
		switch e.Type {
		case "session.start":
			var d struct {
				SessionID string `json:"sessionId"`
				StartTime string `json:"startTime"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				if d.SessionID != "" {
					s.ID = d.SessionID
				}
				if t := parseTimeAny(d.StartTime); !t.IsZero() {
					s.Started = t
					s.Touch(t)
				}
			}
		case "user.message":
			var d struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				if text := strings.TrimSpace(d.Content); text != "" {
					s.Touch(at)
					s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: at})
					if s.Title == "" {
						s.Title = firstLineTrim(text)
					}
				}
			}
		case "assistant.message":
			var d struct {
				Content      string `json:"content"`
				ToolRequests []struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"toolRequests"`
			}
			if json.Unmarshal(e.Data, &d) != nil {
				continue
			}
			if text := strings.TrimSpace(d.Content); text != "" {
				s.Touch(at)
				s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: at})
				if s.Title == "" {
					s.Title = firstLineTrim(text)
				}
			}
			// The arguments are a JSON *string*, which is how the extension
			// writes them here.
			for _, req := range d.ToolRequests {
				if req.Arguments == "" {
					continue
				}
				var args map[string]any
				if json.Unmarshal([]byte(req.Arguments), &args) != nil {
					continue
				}
				copilotAgentToolArgs(&s, req.Name, args, at, addPath)
			}
		case "tool.execution_start":
			var d struct {
				ToolName  string         `json:"toolName"`
				Arguments map[string]any `json:"arguments"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				copilotAgentToolArgs(&s, d.ToolName, d.Arguments, at, addPath)
			}
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	if len(files) > 0 {
		s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(files, "\n"), Time: s.Updated})
	}
	// The transcript records no working directory of its own, so the project
	// comes from the `workspace.json` VS Code keeps beside the storage
	// directory — two levels further up than for a `chatSessions` file — and
	// from the paths the tool calls touched when that file says nothing (the
	// answer claude's reader takes for the same question).
	s.Project = copilotAgentProject(path, s.Messages)
	return []model.Session{s}, nil
}

// copilotAgentProject names the project a transcript belongs to.
//
// The storage directory is `workspaceStorage/<md5 of the workspace URI>`, so
// the hash itself says nothing; `workspace.json` beside it carries the URI, and
// that is two levels above a transcript rather than the one above a
// `chatSessions` file. Failing that, the absolute paths the session's tool
// calls touched name it — the reporter's machine has twelve workspaces and the
// hash for each of them (#3637).
func copilotAgentProject(path string, ms []model.Message) string {
	ws := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	// copilotChatProjectFromWorkspace reads `workspace.json` two levels above
	// the path it is given, which is what a `chatSessions/<id>.json` needs.
	if p := copilotChatProjectFromWorkspace(filepath.Join(ws, "x", "y")); p != "" {
		return p
	}
	if p := projectFromPaths(ms); p != "" {
		return p
	}
	return "-"
}

// copilotAgentToolArgs takes what a tool call says about the work: the file it
// touched, and the command it ran. A command is what `deja how` and `deja fix`
// are built on, so it is filed as one rather than as prose.
func copilotAgentToolArgs(s *model.Session, tool string, args map[string]any, at time.Time, addPath func(string)) {
	if args == nil {
		return
	}
	for _, key := range []string{"filePath", "file_path", "path", "uri"} {
		if v, ok := args[key].(string); ok {
			addPath(chatResourcePath(v))
		}
	}
	if v, ok := args["filePaths"].([]any); ok {
		for _, one := range v {
			if p, ok := one.(string); ok {
				addPath(chatResourcePath(p))
			}
		}
	}
	for _, key := range []string{"command", "commandLine"} {
		if v, ok := args[key].(string); ok {
			if cmd := strings.TrimSpace(v); cmd != "" {
				s.Touch(at)
				s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + firstLineTrim(cmd), Time: at})
				return
			}
		}
	}
	_ = tool
}
