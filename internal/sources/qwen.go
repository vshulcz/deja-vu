package sources

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// QwenConfigDir is the native Qwen Code configuration directory. DEJA_QWEN_ROOT
// intentionally does not affect it because that variable only relocates reads.
func QwenConfigDir() string { return filepath.Join(Home(), ".qwen") }

func QwenRoot() string { return EnvPath("DEJA_QWEN_ROOT", QwenConfigDir()) }

func QwenSessionFiles() []string {
	return walkFiles(filepath.Join(QwenRoot(), "projects"), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl") && filepath.Base(filepath.Dir(p)) == "chats"
	})
}

func LoadQwen() []model.Session { return parseFiles(QwenSessionFiles(), ParseQwenFile) }

func ParseQwenFile(path string) ([]model.Session, error) {
	return parseQwenFileFromOffset(path, 0)
}

func ParseQwenFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseQwenFileFromOffset(path, offset)
}

func parseQwenFileFromOffset(path string, offset int64) ([]model.Session, error) {
	project := projectDir(filepath.Join(QwenRoot(), "projects"), path)
	s := model.Session{
		Harness: "qwen",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: claudeProjectName(project),
		Path:    path,
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if typ != "user" && typ != "assistant" {
			return
		}
		if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		role := typ
		text := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				switch r {
				case "model":
					role = "assistant"
				case "user":
					role = "user"
				default:
					role = typ
				}
			}
			text = qwenText(msg["parts"])
		}
		if text != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		}
		// The work sits in the same parts list, as functionCall and
		// functionResponse rather than text, so qwenText walked past it and
		// the commands a session ran were reachable from nothing.
		if msg, ok := m["message"].(map[string]any); ok {
			s.Messages = append(s.Messages, qwenWorkRecords(msg["parts"], t)...)
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// qwenDialect is Qwen Code's tool vocabulary. The names follow Gemini's — Qwen
// Code is built in that shape — with `file_path` for the file tools and
// `command` for the shell.
var qwenDialect = toolDialect{
	pathKey:   "file_path",
	pathTools: map[string]bool{"read_file": true, "write_file": true, "replace": true, "read_many_files": true},
	shellTool: "run_shell_command",
	editTools: map[string]bool{"replace": true},
	oldKey:    "old_string",
}

// qwenWorkRecords turns the functionCall and functionResponse parts of one
// message into work records. The parts are rewritten into the tool_use shape
// the shared extractors read, so Qwen does not need its own copy of the
// extraction.
func qwenWorkRecords(v any, t time.Time) []model.Message {
	parts, ok := v.([]any)
	if !ok {
		return nil
	}
	var calls []any
	var results []string
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if call, ok := m["functionCall"].(map[string]any); ok {
			name, _ := call["name"].(string)
			args, _ := call["args"].(map[string]any)
			if name != "" && args != nil {
				calls = append(calls, map[string]any{
					"type": "tool_use", "name": name, "input": args,
				})
			}
		}
		if resp, ok := m["functionResponse"].(map[string]any); ok {
			r, _ := resp["response"].(map[string]any)
			out, _ := r["output"].(string)
			if out = strings.TrimSpace(out); out != "" {
				results = append(results, out)
			}
		}
	}
	var recs []model.Message
	if len(calls) > 0 {
		if IndexToolPaths() {
			if p := toolPathsIn(calls, qwenDialect); p != "" {
				recs = append(recs, model.Message{Role: RoleFiles, Text: p, Time: t})
			}
		}
		if IndexEdits() {
			for _, span := range editSpansIn(calls, qwenDialect) {
				recs = append(recs, model.Message{Role: RoleEdit, Text: span, Time: t})
			}
		}
		if IndexCommands() {
			for _, cmd := range commandsIn(calls, qwenDialect) {
				recs = append(recs, model.Message{Role: RoleCommand, Text: cmd, Time: t})
			}
		}
	}
	if IndexToolOutput() {
		for _, out := range results {
			recs = append(recs, model.Message{Role: RoleToolOutput, Text: out, Time: t})
		}
	}
	return recs
}

func qwenText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if thought, _ := m["thought"].(bool); thought {
			continue
		}
		text, _ := m["text"].(string)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String()
}
