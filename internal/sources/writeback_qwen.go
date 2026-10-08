package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Qwen Code: projects/<encoded dir>/chats/<id>.jsonl, one line per turn
// chained by parentUuid, with the parts list Gemini's API takes. `qwen -r
// <id>` finds a session only from the directory its folder encodes, which is
// where `deja resume` runs it; each record names that directory as cwd when
// deja can resolve it.
func init() {
	registerWriteBack("qwen", qwenWriteBackRoots, renderQwen)
}

func qwenWriteBackRoots() []string {
	return []string{filepath.Join(QwenRoot(), "projects")}
}

func renderQwen(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Kind == "subagent" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is a sub-agent run, which Qwen Code does not reopen on its own"}
	}
	if filepath.Base(filepath.Dir(s.Path)) != "chats" || !strings.HasSuffix(s.Path, ".jsonl") || strings.TrimSuffix(filepath.Base(s.Path), ".jsonl") != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no chats/<id>.jsonl path for this session, which is where `qwen -r` looks"}
	}
	cwd, _ := QwenSessionDir(s.Path)
	var lines []any
	parent := any(nil)
	for i, m := range turns {
		id := writeBackUUID(s.ID, i)
		line := map[string]any{
			"uuid":       id,
			"parentUuid": parent,
			"sessionId":  s.ID,
			"timestamp":  writeBackStamp(m.Time),
			"type":       m.Role,
		}
		if cwd != "" {
			line["cwd"] = cwd
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		line["message"] = map[string]any{"role": role, "parts": []any{map[string]any{"text": m.Text}}}
		lines = append(lines, line)
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}
