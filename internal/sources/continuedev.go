package sources

import (
	"encoding/json"
	"net/url"
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

// ContinueSessionDir is the workspace a session ran in: workspaceDirectory on
// the session file, else on its sessions.json entry. The IDE writes it as a
// file URI, the CLI as a path. "" when neither names an absolute path (#4375).
func ContinueSessionDir(path string) string {
	var doc struct {
		SessionID          string `json:"sessionId"`
		WorkspaceDirectory string `json:"workspaceDirectory"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &doc) != nil {
		return ""
	}
	ws := doc.WorkspaceDirectory
	if ws == "" {
		id := doc.SessionID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		ws = continueList(filepath.Dir(path))[id].WorkspaceDirectory
	}
	if strings.HasPrefix(ws, "file://") {
		u, err := url.Parse(ws)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return ""
		}
		ws = u.Path
		// file:///C:/proj is /C:/proj once parsed.
		if len(ws) > 2 && ws[0] == '/' && ws[2] == ':' {
			ws = ws[1:]
		}
		ws = filepath.FromSlash(ws)
	}
	if !filepath.IsAbs(ws) {
		return ""
	}
	return ws
}

type continueSession struct {
	SessionID          string              `json:"sessionId"`
	Title              string              `json:"title"`
	WorkspaceDirectory string              `json:"workspaceDirectory"`
	History            []continueHistoryIt `json:"history"`
}

// A history item is a message plus, on the assistant side, the tool calls it
// made: the call in message.toolCalls, and its arguments, status and output in
// toolCallStates on the item itself (#4373).
type continueHistoryIt struct {
	Message        continueMessage      `json:"message"`
	ToolCallStates []continueToolCallSt `json:"toolCallStates"`
}

type continueToolCallSt struct {
	Status     string         `json:"status"`
	ParsedArgs map[string]any `json:"parsedArgs"`
	ToolCall   struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"toolCall"`
	Output []struct {
		Content string `json:"content"`
	} `json:"output"`
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
		s.Project = projectName(workspace)
	} else {
		s.Project = "continue"
	}
	if s.Title == "" {
		s.Title = firstLineTrim(entry.Title)
	}

	var mtime time.Time
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime()
	}
	base := continueDate(entry.DateCreated)
	if base.IsZero() {
		base = mtime
	}
	// A second a turn, unless that runs past the file's mtime: a fork copies the
	// whole history under a fresh dateCreated, and 17 items one second apart
	// dated the last turn 16 s after the file was written. Then the turns are
	// spread between the start and the mtime instead (#4376). With no start
	// before the mtime to spread from — no dateCreated, so the start is the
	// mtime — the session ends at the mtime, a second a turn: one instant for
	// every turn lost their order, and the same words said twice were deduped
	// as one.
	step := time.Second
	if n := len(doc.History); n > 1 && !mtime.IsZero() && base.Add(time.Duration(n-1)*step).After(mtime) {
		if mtime.After(base) {
			step = mtime.Sub(base) / time.Duration(n-1)
		} else {
			base = mtime.Add(-time.Duration(n-1) * step)
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
		at := base.Add(time.Duration(i) * step)
		if text := continueText(it.Message.Content); text != "" {
			s.Touch(at)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at})
		}
		if work := continueToolWork(it.ToolCallStates, at); len(work) > 0 {
			s.Touch(at)
			s.Messages = append(s.Messages, work...)
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	if continuePlaceholderTitle(s.Title, s.Messages) {
		// Continue's own stand-ins and a title that is only the first line
		// typed are no titles: left empty, the index derives one and looks
		// past a greeting the way it does for every other harness (#3274).
		s.Title = ""
	}
	return []model.Session{s}, nil
}

// continueToolWork turns an item's tool calls into work records. The names are
// the CLI's (Bash, Read, Write, Edit, MultiEdit) and the IDE extension's
// (run_terminal_command, read_file, create_new_file, single_find_and_replace,
// multi_edit); the CLI's Edit takes file_path and the rest filepath. A call
// Continue marked errored changed nothing, so it leaves its path and its
// output — the failure — but no edit or written lines.
func continueToolWork(states []continueToolCallSt, at time.Time) []model.Message {
	var out, outputs []model.Message
	var paths []string
	for _, st := range states {
		name := st.ToolCall.Function.Name
		args := st.ParsedArgs
		if args == nil {
			_ = json.Unmarshal([]byte(st.ToolCall.Function.Arguments), &args)
		}
		path := strings.TrimSpace(str(args["filepath"]))
		if path == "" {
			path = strings.TrimSpace(str(args["file_path"]))
		}
		switch name {
		case "Bash", "run_terminal_command":
			if cmd := strings.TrimSpace(str(args["command"])); IndexCommands() && cmd != "" && worthIndexing(cmd) {
				out = append(out, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: at})
			}
			path = ""
		case "Edit", "MultiEdit", "Write", "single_find_and_replace", "multi_edit", "create_new_file":
			if st.Status == "errored" || path == "" || strings.ContainsAny(path, "\n\r") {
				break
			}
			olds := []string{str(args["old_string"])}
			news := []string{str(args["new_string"]), str(args["content"]), str(args["contents"])}
			if edits, ok := args["edits"].([]any); ok {
				for _, e := range edits {
					if m, ok := e.(map[string]any); ok {
						olds = append(olds, str(m["old_string"]))
						news = append(news, str(m["new_string"]))
					}
				}
			}
			for _, span := range olds {
				if IndexEdits() && span != "" {
					if len(span) > editSpanMax {
						span = span[:editSpanMax]
					}
					out = append(out, model.Message{Role: RoleEdit, Text: path + "\n" + span, Time: at})
				}
			}
			for _, w := range news {
				if rec := WroteRecord(path, w); IndexWrites() && rec != "" {
					out = append(out, model.Message{Role: RoleWrote, Text: rec, Time: at})
				}
			}
		}
		if path != "" {
			paths = append(paths, path)
		}
		if IndexToolOutput() {
			for _, o := range st.Output {
				if t := strings.TrimSpace(o.Content); t != "" {
					outputs = append(outputs, model.Message{Role: RoleToolOutput, Text: capParsedMessage(t), Time: at})
				}
			}
		}
	}
	if len(paths) > 0 && IndexToolPaths() {
		out = append(out, model.Message{Role: RoleFiles, Text: strings.Join(dedupeStrings(paths), "\n"), Time: at})
	}
	return append(out, outputs...)
}

// continuePlaceholderTitle reports whether a title is Continue's placeholder —
// "New Session" (NEW_SESSION_TITLE) or the CLI's "Untitled Session"
// (DEFAULT_SESSION_TITLE) — or just the first user line again.
func continuePlaceholderTitle(title string, ms []model.Message) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	switch strings.ToLower(t) {
	case "new session", "untitled session":
		return true
	}
	for _, m := range ms {
		if m.Role == "user" {
			return strings.EqualFold(t, firstLineTrim(m.Text))
		}
	}
	return false
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
