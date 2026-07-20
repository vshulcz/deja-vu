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
