package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CopilotRoot returns the GitHub Copilot CLI session-state root, overridable
// via DEJA_COPILOT_ROOT. Each session lives in its own UUID directory as an
// append-only events.jsonl.
func CopilotRoot() string {
	return EnvPath("DEJA_COPILOT_ROOT", filepath.Join(Home(), ".copilot", "session-state"))
}

// CopilotSessionFiles lists event logs under the Copilot session root.
func CopilotSessionFiles() []string {
	return walkFiles(CopilotRoot(), func(p string) bool {
		return filepath.Base(p) == "events.jsonl"
	})
}

// LoadCopilot loads all Copilot CLI sessions.
func LoadCopilot() []model.Session { return parseFiles(CopilotSessionFiles(), ParseCopilotFile) }

// ParseCopilotFile parses a single Copilot events.jsonl.
func ParseCopilotFile(path string) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, 0)
}

// ParseCopilotFileFromOffset parses a Copilot event log starting at a byte offset.
func ParseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, offset)
}

// copilotDialect is Copilot's tool vocabulary, read off events.jsonl its own
// CLI wrote. Names are lowercase where Claude's are capitalised, the file key
// is `path`, and the replaced span is `old_str` rather than `old_string` —
// reading the wrong one loses the only record of what stopped existing.
var copilotDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"edit": true, "read": true, "write": true, "create": true},
	shellTool: "bash",
	editTools: map[string]bool{"edit": true},
	oldKey:    "old_str",
}

func parseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "copilot",
		ID:      filepath.Base(filepath.Dir(path)),
		Path:    path,
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		data, _ := m["data"].(map[string]any)
		t := parseTimeAny(m["timestamp"])
		switch typ {
		case "session.start":
			if data == nil {
				return
			}
			if id, _ := data["sessionId"].(string); id != "" {
				s.ID = id
			}
			s.Touch(parseTimeAny(data["startTime"]))
			if ctx, ok := data["context"].(map[string]any); ok {
				if cwd, _ := ctx["cwd"].(string); cwd != "" {
					s.Project = copilotProjectName(cwd)
				}
			}
		case "user.message", "assistant.message":
			if data == nil {
				return
			}
			role := "user"
			if typ == "assistant.message" {
				role = "assistant"
			}
			s.Touch(t)
			if txt, _ := data["content"].(string); txt != "" {
				s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
			}
		case "tool.execution_start":
			// The call carries the work: which file, what it replaced, what
			// command ran. Copilot files these outside the message stream, so
			// the assistant turns that do nothing but call tools were being
			// stored as empty and the work was reachable from nothing.
			if data == nil {
				return
			}
			name, _ := data["toolName"].(string)
			args, _ := data["arguments"].(map[string]any)
			if name == "" || args == nil {
				return
			}
			part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
			var records []model.Message
			if IndexToolPaths() {
				if p := toolPathsIn(part, copilotDialect); p != "" {
					records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexEdits() {
				for _, span := range editSpansIn(part, copilotDialect) {
					records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
				}
			}
			if IndexCommands() {
				for _, cmd := range commandsIn(part, copilotDialect) {
					records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
				}
			}
			if len(records) == 0 {
				return
			}
			s.Touch(t)
			s.Messages = append(s.Messages, records...)
		case "tool.execution_complete":
			// Kept whether or not the call succeeded: the error a command hit
			// is exactly what a later search reaches for, and copilot's own
			// failure text was unreachable before this.
			if !IndexToolOutput() || data == nil {
				return
			}
			result, _ := data["result"].(map[string]any)
			out, _ := result["content"].(string)
			if out = strings.TrimSpace(out); out == "" {
				return
			}
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// copilotProjectName mirrors the codex convention: the last two path segments
// of the recorded working directory, or the final one at filesystem roots.
func copilotProjectName(cwd string) string {
	cwd = strings.TrimRight(cwd, "/\\")
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	parent := filepath.Base(filepath.Dir(cwd))
	if parent != "" && parent != "." && parent != string(filepath.Separator) && !strings.Contains(parent, ":") {
		return parent + "/" + base
	}
	return base
}
