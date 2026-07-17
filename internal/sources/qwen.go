package sources

import (
	"path/filepath"
	"strings"

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
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
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
