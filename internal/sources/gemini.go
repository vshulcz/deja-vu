package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Gemini CLI records chats under ~/.gemini/tmp/<projectId>/chats/ in two
// generations: whole-session JSON files and newer JSONL logs where the first
// line is metadata and later lines are messages, "$set" metadata patches, or
// "$rewindTo" truncation markers. Project ids are either path-hashes or
// slugs; ~/.gemini/projects.json maps real paths to ids.

func GeminiRoot() string {
	return EnvPath("DEJA_GEMINI_ROOT", filepath.Join(Home(), ".gemini"))
}

func GeminiChatFiles() []string {
	root := filepath.Join(GeminiRoot(), "tmp")
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		chats := filepath.Join(root, e.Name(), "chats")
		_ = filepath.WalkDir(chats, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonl")) {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func LoadGemini() []model.Session {
	ss := parseFiles(GeminiChatFiles(), ParseGeminiFile)
	return dedupeGeminiSessions(ss)
}

// A session resumed from an old .json gets rewritten as .jsonl — keep the
// jsonl (richer, current) when both exist.
func dedupeGeminiSessions(ss []model.Session) []model.Session {
	best := map[string]int{}
	var out []model.Session
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		if i, ok := best[key]; ok {
			if strings.HasSuffix(s.Path, ".jsonl") {
				out[i] = s
			}
			continue
		}
		best[key] = len(out)
		out = append(out, s)
	}
	return out
}

func ParseGeminiFile(path string) ([]model.Session, error) {
	if strings.HasSuffix(path, ".jsonl") {
		return parseGeminiJSONL(path)
	}
	return parseGeminiJSON(path)
}

type geminiMessage struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	Model     string          `json:"model"`
}

func parseGeminiJSON(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		SessionID   string          `json:"sessionId"`
		StartTime   string          `json:"startTime"`
		LastUpdated string          `json:"lastUpdated"`
		Messages    []geminiMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, nil // not a session file (e.g. checkpoint) — skip quietly
	}
	if doc.SessionID == "" {
		return nil, nil
	}
	s := geminiSessionShell(path, doc.SessionID, doc.StartTime, doc.LastUpdated)
	appendGeminiMessages(&s, doc.Messages)
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

func parseGeminiJSONL(path string) ([]model.Session, error) {
	var s model.Session
	started := false
	var msgs []geminiMessage
	err := scanJSONLFromOffset(path, 0, func(m map[string]any) {
		if !started {
			id, _ := m["sessionId"].(string)
			if id == "" {
				return
			}
			st, _ := m["startTime"].(string)
			lu, _ := m["lastUpdated"].(string)
			s = geminiSessionShell(path, id, st, lu)
			started = true
			return
		}
		if patch, ok := m["$set"].(map[string]any); ok {
			if lu, _ := patch["lastUpdated"].(string); lu != "" {
				if t, err := time.Parse(time.RFC3339Nano, lu); err == nil {
					s.Touch(t)
				}
			}
			return
		}
		if rid, ok := m["$rewindTo"].(string); ok {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].ID == rid {
					msgs = msgs[:i]
					break
				}
			}
			return
		}
		raw, _ := json.Marshal(m)
		var gm geminiMessage
		if json.Unmarshal(raw, &gm) == nil && gm.Type != "" {
			msgs = append(msgs, gm)
		}
	})
	if !started {
		return nil, err
	}
	appendGeminiMessages(&s, msgs)
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func geminiSessionShell(path, id, startTime, lastUpdated string) model.Session {
	s := model.Session{Harness: "gemini", ID: id, Project: geminiProjectName(path), Path: path}
	if t, err := time.Parse(time.RFC3339Nano, startTime); err == nil {
		s.Touch(t)
	}
	if t, err := time.Parse(time.RFC3339Nano, lastUpdated); err == nil {
		s.Touch(t)
	}
	return s
}

func appendGeminiMessages(s *model.Session, msgs []geminiMessage) {
	for _, m := range msgs {
		role := ""
		switch m.Type {
		case "user":
			role = "user"
		case "gemini", "model":
			role = "assistant"
		default:
			continue // info/error/warning noise
		}
		text := geminiContentText(m.Content)
		if text == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, m.Timestamp)
		if t.IsZero() {
			t = s.Started
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	}
}

// content is a string or an array of Part objects ({"text": ...} и др.)
func geminiContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if t, _ := p["text"].(string); t != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}

// geminiProjectName resolves .gemini/tmp/<id>/chats/x -> a display name:
// projects.json reverse mapping first, then a .project_root marker, then the
// raw id (slug or hash).
func geminiProjectName(path string) string {
	idDir := filepath.Dir(filepath.Dir(path)) // .../tmp/<id>
	// subagent files nest one deeper: chats/<parent>/<sid>.jsonl
	if filepath.Base(filepath.Dir(path)) != "chats" && filepath.Base(idDir) == "chats" {
		idDir = filepath.Dir(idDir)
	}
	id := filepath.Base(idDir)
	if mapped := geminiProjectFromRegistry(id); mapped != "" {
		return mapped
	}
	if b, err := os.ReadFile(filepath.Join(idDir, ".project_root")); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return projectName(p)
		}
	}
	return id
}

func geminiProjectFromRegistry(id string) string {
	b, err := os.ReadFile(filepath.Join(GeminiRoot(), "projects.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Projects map[string]string `json:"projects"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return ""
	}
	for path, pid := range doc.Projects {
		if pid == id {
			return projectName(path)
		}
	}
	return ""
}
