package sources

// CodeArts Desktop (Huawei Cloud CodeArts) keeps one session in a two-level
// directory tree: ~/.codearts/kernel/sessions/<launch-bucket>/<session-uuid>/
// with meta.json (session metadata) and history.jsonl (one message per line).
//
// The message shape is Rust serde variants:
//
//	{"role":"User"|"Assistant"|"Tool",
//	 "content":{"SinglePart":{"Text":{"text":"..."}}}
//	          |{"MultiPart":[{"Text":{...}}|{"ToolCall":{...}}|{"ToolResult":{...}}]},
//	 "reasoning_content":"...", "timestamp":"RFC3339", ...}
//
// deja needs speech, not a replayable transcript: user/assistant Text blocks
// and the assistant's reasoning_content are indexed as messages; tool calls
// and results are work records and are left out (their text is the tool's, not
// the person's). Malformed lines are skipped, matching the other JSONL readers.
//
// Format attested by Nwflower/dsh-chat-import PR #103 (lib/sources/codearts.mjs
// and lib/convert/codearts.mjs) and checked against a real 0.2.8 store.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const codeartsHistoryFile = "history.jsonl"
const codeartsMetaFile = "meta.json"

// CodeArtsRoot is the store root. DEJA_CODEARTS_ROOT replaces it; CODEARTS_HOME
// (absolute only, matching chat-import's isAbsoluteLike rule) places the store
// under <CODEARTS_HOME>/kernel/sessions.
func CodeArtsRoot() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_CODEARTS_ROOT")); p != "" {
		return p
	}
	if h := strings.TrimSpace(os.Getenv("CODEARTS_HOME")); h != "" && filepath.IsAbs(h) {
		return filepath.Join(h, "kernel", "sessions")
	}
	return filepath.Join(Home(), ".codearts", "kernel", "sessions")
}

// CodeArtsFiles lists history.jsonl files under the two-level bucket tree.
// Index files (messages.index.jsonl) are not sessions.
func CodeArtsFiles() []string {
	return walkFiles(CodeArtsRoot(), func(p string) bool {
		return filepath.Base(p) == codeartsHistoryFile
	})
}

// IsCodeArtsHistory reports whether p is a CodeArts transcript.
func IsCodeArtsHistory(p string) bool {
	return strings.HasPrefix(p, CodeArtsRoot()) && filepath.Base(p) == codeartsHistoryFile
}

type codeartsMeta struct {
	SessionID        string `json:"session_id"`
	Title            string `json:"title"`
	WorkingDirectory string `json:"working_directory"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type codeartsText struct {
	Text string `json:"text"`
}

type codeartsPart struct {
	Text       *codeartsText `json:"Text"`
	ToolCall   any           `json:"ToolCall"`
	ToolResult any           `json:"ToolResult"`
}

type codeartsContent struct {
	SinglePart *codeartsPart  `json:"SinglePart"`
	MultiPart  []codeartsPart `json:"MultiPart"`
}

type codeartsLine struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   codeartsContent `json:"content"`
	Timestamp string          `json:"timestamp"`
}

func codeartsParts(c codeartsContent) []codeartsPart {
	if c.SinglePart != nil {
		return []codeartsPart{*c.SinglePart}
	}
	return c.MultiPart
}

// codeartsTime parses RFC3339 with optional fractional seconds; a bad stamp
// leaves the message at zero time rather than dropping the whole line.
func codeartsTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ParseCodeArtsFile reads one session directory's history.jsonl and meta.json.
func ParseCodeArtsFile(path string) ([]model.Session, error) {
	return parseCodeArtsFileFromOffset(path, 0)
}

// ParseCodeArtsFileFromOffset is the incremental read: bytes appended after
// offset are folded into the same session, with meta.json re-read each time so
// a resumed parse keeps the id, title and project (#2870 pattern).
func parseCodeArtsFileFromOffset(path string, offset int64) ([]model.Session, error) {
	dir := filepath.Dir(path)
	s := model.Session{
		Harness: "codearts",
		ID:      filepath.Base(dir),
		Path:    path,
	}
	metaPath := filepath.Join(dir, codeartsMetaFile)
	if raw, err := os.ReadFile(metaPath); err == nil {
		var m codeartsMeta
		if json.Unmarshal(raw, &m) == nil {
			if m.SessionID != "" {
				s.ID = m.SessionID
			}
			if m.Title != "" {
				s.Title = m.Title
			}
			if m.WorkingDirectory != "" {
				s.Project = projectName(m.WorkingDirectory)
			}
			t := codeartsTime(m.CreatedAt)
			if !t.IsZero() {
				s.Started = t
			}
			t = codeartsTime(m.UpdatedAt)
			if !t.IsZero() {
				s.Updated = t
			}
		}
	}
	if s.Started.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			s.Started = fi.ModTime()
		}
	}

	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		codeartsLineInto(&s, m)
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	if s.Updated.IsZero() && len(s.Messages) > 0 {
		s.Updated = s.Messages[len(s.Messages)-1].Time
	}
	return []model.Session{s}, err
}

// codeartsLineInto folds one JSONL record into s. The map form is what
// scanJSONLFromOffset hands over; re-marshalling keeps the variant unpacking
// in one place instead of hand-walking nested maps.
func codeartsLineInto(s *model.Session, m map[string]any) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	var line codeartsLine
	if json.Unmarshal(raw, &line) != nil {
		return
	}
	role := strings.ToLower(line.Role)
	t := codeartsTime(line.Timestamp)
	if !t.IsZero() {
		s.Touch(t)
	}
	switch role {
	case "user", "assistant":
		// reasoning_content is the model thinking out loud; deja indexes only
		// what was said (same rule as the deepseek and qwen readers).
		for _, p := range codeartsParts(line.Content) {
			if p.Text == nil || p.Text.Text == "" {
				continue
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: p.Text.Text, Time: t})
		}
	default:
		// Tool calls and results are work records, not speech. deja's index
		// keeps command/error text from other harnesses via RoleToolOutput;
		// CodeArts ToolResult bodies are renderer output and are skipped here.
		return
	}
}

// LoadCodeArts is the cold load for the registry.
func LoadCodeArts() []model.Session {
	return parseFiles(CodeArtsFiles(), ParseCodeArtsFile)
}
