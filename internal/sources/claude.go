package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

func ClaudeRoot() string {
	return EnvPath("DEJA_CLAUDE_ROOT", filepath.Join(Home(), ".claude", "projects"))
}

func LoadClaude() []model.Session {
	root := ClaudeRoot()
	files := walkFiles(root, func(p string) bool { return strings.HasSuffix(p, ".jsonl") })
	return parseFiles(files, ParseClaudeFile)
}

func ParseClaudeFile(path string) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, 0)
}

func ParseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseClaudeFileFromOffset(path, offset)
}

func parseClaudeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Project: claudeProjectName(claudeProjectDir(path)), Path: path}
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
		txt := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				role = r
			}
			txt = textFromContent(msg["content"])
		}
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func claudeProjectDir(path string) string {
	root := ClaudeRoot()
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 1 && parts[0] != "" {
			return filepath.Join(root, parts[0])
		}
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		project := filepath.Dir(filepath.Dir(dir))
		if project != "." && project != string(filepath.Separator) {
			return project
		}
	}
	return dir
}

func claudeProjectName(dir string) string {
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "-"
	}
	parts := strings.Split(base, "-")
	var clean []string
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return base
	}
	if len(clean) == 1 {
		return clean[0]
	}
	return filepath.Join(clean[len(clean)-2], clean[len(clean)-1])
}
