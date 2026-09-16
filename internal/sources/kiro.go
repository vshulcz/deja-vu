package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kiro (kiro.dev, Amazon's agent) keeps two file stores under ~/.kiro/sessions,
// written by its two clients, and they are different formats.
//
// The CLI writes a pair per session:
//
//	cli/<sessionId>.json   header — session_id, cwd, the model and turn metadata
//	cli/<sessionId>.jsonl  the conversation, one record per line
//
// A record is `{"kind":"Prompt"|"AssistantMessage","data":{…}}` with the text in
// `data.content[].data` under `kind:"text"`, and `data.meta.timestamp` in
// seconds. Several `AssistantMessage` records can share one `data.message_id`:
// the client appends the reply as it streams, and each record carries the next
// piece rather than the whole answer so far. Read one per record and a recall
// quotes a third of a sentence, so a run under one id is joined in order
// (tokscale's reader sums the same records for the same reason —
// crates/tokscale-core/src/sessions/kiro.rs).
//
// The IDE — Kiro is a VS Code fork — writes per workspace instead:
//
//	<workspace>/sess_<uuid>/session.json    id, modelId, workspacePaths, createdAt
//	<workspace>/sess_<uuid>/messages.jsonl  the conversation
//
// where a line is `{"timestamp":<RFC3339>,"payload":{"type":"user"|"assistant",
// "content":"…"}}`. Older builds wrote the flat `{"role":…,"content":…}` shape
// into the same file, so both are read.
//
// What is not read yet, and why: the IDE also mirrors chats into its
// globalStorage (`kiro.kiroagent/<workspace>/*.chat` beside extensionless
// execution records) and the TUI keeps sessions in a SQLite store
// (`kiro-cli/data.sqlite3`, table `conversations_v2`). Both are shapes no
// sample here covers — the store is not on this machine, and a reader written
// against a guess is a reader that silently drops history (#3103).

// KiroRoot is the session store root. DEJA_KIRO_ROOT replaces it.
func KiroRoot() string {
	return EnvPath("DEJA_KIRO_ROOT", filepath.Join(Home(), ".kiro", "sessions"))
}

// KiroCLIFiles lists the CLI transcripts: the .jsonl half of each pair.
func KiroCLIFiles() []string {
	return walkFiles(filepath.Join(KiroRoot(), "cli"), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// KiroIDEFiles lists the IDE transcripts, which are named rather than numbered.
func KiroIDEFiles() []string {
	return walkFiles(KiroRoot(), func(p string) bool {
		return filepath.Base(p) == "messages.jsonl" && kiroUnderSessDir(p)
	})
}

// KiroSessionFiles is everything a Kiro install has on disk.
func KiroSessionFiles() []string {
	return append(KiroCLIFiles(), KiroIDEFiles()...)
}

// KiroUnderCLI reports whether a path is a CLI transcript of this store, so the
// registry can claim it for incremental ingest.
func KiroUnderCLI(p string) bool {
	return strings.HasPrefix(p, filepath.Join(KiroRoot(), "cli")) && strings.HasSuffix(p, ".jsonl")
}

// KiroUnderIDE is the same question for the IDE layout.
func KiroUnderIDE(p string) bool {
	return filepath.Base(p) == "messages.jsonl" &&
		strings.HasPrefix(p, KiroRoot()) && kiroUnderSessDir(p)
}

// kiroUnderSessDir reports whether a transcript sits in a `sess_<uuid>`
// directory, which is what separates an IDE session from anything else under
// the root.
func kiroUnderSessDir(p string) bool {
	return strings.HasPrefix(filepath.Base(filepath.Dir(p)), "sess_")
}

func LoadKiro() []model.Session {
	ss := parseFiles(KiroCLIFiles(), ParseKiroCLIFile)
	return append(ss, parseFiles(KiroIDEFiles(), ParseKiroIDEFile)...)
}

// ParseKiroCLIFile reads one CLI transcript.
func ParseKiroCLIFile(path string) ([]model.Session, error) {
	return ParseKiroCLIFileFromOffset(path, 0)
}

// ParseKiroCLIFileFromOffset is the incremental read. The header is a separate
// file, so it is read on every pass rather than depending on the watermark.
func ParseKiroCLIFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "kiro",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	applyKiroCLIHeader(&s, strings.TrimSuffix(path, ".jsonl")+".json")

	// A streamed reply arrives as several records under one message id. Joining
	// them needs the run to be contiguous, which it is: the client appends as
	// the answer comes in.
	var lastID string
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		kind, _ := m["kind"].(string)
		data, _ := m["data"].(map[string]any)
		if data == nil {
			return
		}
		text := kiroContentText(data["content"])
		if text == "" {
			return
		}
		t := parseTimeAny(kiroMetaTimestamp(data))
		s.Touch(t)
		id, _ := data["message_id"].(string)
		switch kind {
		case "Prompt":
			lastID = ""
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
		case "AssistantMessage":
			if id != "" && id == lastID && len(s.Messages) > 0 {
				last := &s.Messages[len(s.Messages)-1]
				last.Text += text
				return
			}
			lastID = id
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// applyKiroCLIHeader reads identity out of the header file beside the
// transcript: the session's own id and the directory it ran in.
func applyKiroCLIHeader(s *model.Session, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var header struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	if json.Unmarshal(b, &header) != nil {
		return
	}
	if header.SessionID != "" {
		s.ID = header.SessionID
	}
	if header.CWD != "" {
		s.Project = claudeProjectName(pathToProjectKey(header.CWD))
	}
}

// kiroContentText pulls the text out of a CLI record's content array, whose
// parts name their own kind.
func kiroContentText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		return textFromContent(v)
	}
	var b strings.Builder
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := p["kind"].(string); kind != "" && kind != "text" {
			continue
		}
		if s, _ := p["data"].(string); s != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s)
		}
	}
	return b.String()
}

func kiroMetaTimestamp(data map[string]any) any {
	meta, _ := data["meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	return meta["timestamp"]
}

// ParseKiroIDEFile reads one IDE transcript.
func ParseKiroIDEFile(path string) ([]model.Session, error) {
	return ParseKiroIDEFileFromOffset(path, 0)
}

// ParseKiroIDEFileFromOffset is the incremental read.
func ParseKiroIDEFileFromOffset(path string, offset int64) ([]model.Session, error) {
	dir := filepath.Dir(path)
	s := model.Session{
		Harness: "kiro",
		ID:      filepath.Base(dir),
		Project: claudeProjectName(pathToProjectKey(filepath.Base(filepath.Dir(dir)))),
		Path:    path,
	}
	applyKiroIDEHeader(&s, filepath.Join(dir, "session.json"))

	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		t := parseTimeAny(m["timestamp"])
		role, text := kiroIDELine(m)
		if text == "" {
			return
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kiroIDELine reads one IDE record, in either of the two shapes that file has
// held: the payload wrapper of current builds and the flat role/content line of
// the ones before it.
func kiroIDELine(m map[string]any) (string, string) {
	payload, _ := m["payload"].(map[string]any)
	if payload == nil {
		payload = m
	}
	kind, _ := payload["type"].(string)
	if kind == "" {
		kind, _ = payload["role"].(string)
	}
	text := textFromContent(payload["content"])
	switch kind {
	case "user", "human", "prompt":
		return "user", text
	case "assistant", "bot", "response":
		return "assistant", text
	case "tool_result":
		return RoleToolOutput, text
	}
	return "", ""
}

// applyKiroIDEHeader reads the metadata half of an IDE session: its own id and
// the workspace it ran in, which is more precise than the directory name the
// path carries (that one is folded).
func applyKiroIDEHeader(s *model.Session, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var header struct {
		ID             string   `json:"id"`
		WorkspacePaths []string `json:"workspacePaths"`
		CreatedAt      string   `json:"createdAt"`
	}
	if json.Unmarshal(b, &header) != nil {
		return
	}
	if header.ID != "" {
		s.ID = header.ID
	}
	if len(header.WorkspacePaths) > 0 && header.WorkspacePaths[0] != "" {
		s.Project = claudeProjectName(pathToProjectKey(header.WorkspacePaths[0]))
	}
	s.Touch(parseTimeAny(header.CreatedAt))
}
