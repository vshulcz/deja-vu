package sources

import (
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

func CodexRoot() string { return EnvPath("DEJA_CODEX_ROOT", filepath.Join(Home(), ".codex")) }

func LoadCodex() []model.Session {
	root := CodexRoot()
	files := walkFiles(filepath.Join(root, "sessions"), codexRolloutWanted)
	ss := parseFiles(files, ParseCodexRollout)
	if hist, _ := ParseCodexHistory(filepath.Join(root, "history.jsonl")); len(hist) > 0 {
		ss = append(ss, hist...)
	}
	return ss
}

func codexRolloutWanted(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && strings.Contains(filepath.Base(p), "rollout-")
}

// CodexFiles lists the rollout transcripts (plus history.jsonl when present)
// without parsing them — a cheap count for diagnostics.
func CodexFiles() []string {
	root := CodexRoot()
	files := walkFiles(filepath.Join(root, "sessions"), codexRolloutWanted)
	if hist := filepath.Join(root, "history.jsonl"); fileExists(hist) {
		files = append(files, hist)
	}
	return files
}

func ParseCodexHistory(path string) ([]model.Session, error) {
	return ParseCodexHistoryFromOffset(path, 0)
}

func ParseCodexHistoryFromOffset(path string, offset int64) ([]model.Session, error) {
	var out []model.Session
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		id, _ := m["session_id"].(string)
		txt, _ := m["text"].(string)
		if id == "" || txt == "" {
			return
		}
		t := parseTimeAny(m["ts"])
		out = append(out, model.Session{Harness: "codex", ID: id, Project: "history", Path: path, Started: t, Updated: t, Messages: []model.Message{{Role: "user", Text: txt, Time: t}}})
	})
	return out, err
}

func ParseCodexRollout(path string) ([]model.Session, error) {
	return ParseCodexRolloutFromOffset(path, 0)
}

func ParseCodexRolloutFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{Harness: "codex", ID: strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "rollout-"), ".jsonl"), Project: projectName(filepath.Dir(path)), Path: path}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		payload, _ := m["payload"].(map[string]any)
		if payload == nil {
			return
		}
		if typ, _ := m["type"].(string); typ == "session_meta" {
			if id, _ := payload["session_id"].(string); id != "" {
				s.ID = id
			}
			if cwd, _ := payload["cwd"].(string); cwd != "" {
				s.Project = projectName(cwd)
			}
			return
		}
		role, _ := payload["role"].(string)
		txt := textFromContent(payload["content"])
		if txt == "" {
			if msg, _ := payload["message"].(string); msg != "" {
				role = "user"
				txt = msg
			}
		}
		if role != "" && txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}
