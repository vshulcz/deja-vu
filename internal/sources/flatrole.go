package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// parseFlatRoleJSONL reads a transcript whose every line is one message —
// `{"role":…,"content":…,"timestamp":…,"sessionId":…}` — with no envelope
// around it. Command Code and ZCode both write that shape under a
// Claude-Code-style `projects/<encoded-cwd>/<session>.jsonl` layout, which is
// what the two have in common and all they have in common (#3647).
//
// content is either a string or the block array Claude's format uses, so the
// shared textFromContent decides. A line whose role is neither user nor
// assistant is tool output: that is where a command's error text lives, and the
// harnesses that dropped it were the ones whose users could not search for an
// error they had already hit.
func parseFlatRoleJSONL(path string, offset int64, harness, project string) ([]model.Session, error) {
	s := model.Session{
		Harness: harness,
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: project,
		Path:    path,
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		role, _ := m["role"].(string)
		if role == "" {
			return
		}
		text := textFromContent(m["content"])
		if text == "" {
			return
		}
		if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		switch role {
		case "user", "assistant":
		default:
			role = RoleToolOutput
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}
