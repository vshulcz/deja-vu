package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cline (github.com/cline/cline) has two session-store generations, both
// covered by one harness with two file kinds (issue #253):
//
//  1. The current CLI/SDK shared store:
//     ${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/
//     <sessionId>/<sessionId>.json          (manifest: cwd, timestamps, title)
//     <sessionId>/<sessionId>.messages.json (transcript)
//
//  2. The released VS Code extension's legacy store under the host's
//     globalStorage (saoudrizwan.claude-dev):
//     state/taskHistory.json                (task metadata list)
//     tasks/<taskId>/api_conversation_history.json (transcript)
//
// Both transcript formats are whole-file JSON rewritten on change, not
// append-only logs, so there is no incremental ParseFrom. Only user and
// assistant text blocks are indexed; tool payloads, thinking, files, images,
// subagents and compaction artifacts are skipped by design. Modern messages
// files are indexed only for the lead agent.

// ClineConfigDir is the native modern data root (~/.cline/data by default),
// following Cline's own precedence chain.
func ClineConfigDir() string {
	if p := os.Getenv("CLINE_DATA_DIR"); p != "" {
		return p
	}
	if p := os.Getenv("CLINE_DIR"); p != "" {
		return filepath.Join(p, "data")
	}
	return filepath.Join(Home(), ".cline", "data")
}

// ClineSessionsDir is the modern shared session store. DEJA_CLINE_ROOT
// relocates reads only, mirroring every other harness.
func ClineSessionsDir() string {
	if p := os.Getenv("DEJA_CLINE_ROOT"); p != "" {
		return p
	}
	if p := os.Getenv("CLINE_SESSION_DATA_DIR"); p != "" {
		return p
	}
	return filepath.Join(ClineConfigDir(), "sessions")
}

// ClineMCPSettingsPath is where `deja install cline` writes the MCP entry.
func ClineMCPSettingsPath() string {
	if p := os.Getenv("CLINE_MCP_SETTINGS_PATH"); p != "" {
		return p
	}
	return filepath.Join(ClineConfigDir(), "settings", "cline_mcp_settings.json")
}

// ClineLegacyRoots enumerates the VS Code-compatible hosts' extension
// global-storage directories. The extension has no native relocation
// variable, so DEJA_CLINE_ROOTS (a path list) is the read override.
func ClineLegacyRoots() []string {
	if list := os.Getenv("DEJA_CLINE_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	const ext = "saoudrizwan.claude-dev"
	var bases []string
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(Home(), "Library", "Application Support")
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(cfg, host, "User", "globalStorage", ext))
		}
	}
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

// ClineSessionFiles lists both generations' transcript files.
func ClineSessionFiles() []string {
	files := walkFiles(ClineSessionsDir(), func(p string) bool {
		return strings.HasSuffix(p, ".messages.json")
	})
	for _, root := range ClineLegacyRoots() {
		files = append(files, walkFiles(filepath.Join(root, "tasks"), func(p string) bool {
			return filepath.Base(p) == "api_conversation_history.json"
		})...)
	}
	return files
}

func LoadCline() []model.Session {
	return parseFiles(ClineSessionFiles(), ParseClineFile)
}

// ParseClineFile dispatches on the file kind.
func ParseClineFile(path string) ([]model.Session, error) {
	if filepath.Base(path) == "api_conversation_history.json" {
		return parseClineLegacyTask(path)
	}
	return parseClineModernSession(path)
}

// --- modern CLI/SDK store ---

type clineManifest struct {
	SessionID     string `json:"session_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	CWD           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
	Prompt        string `json:"prompt"`
	Metadata      struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

type clineMessages struct {
	Agent    string `json:"agent"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		TS      int64           `json:"ts"`
	} `json:"messages"`
}

func parseClineModernSession(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs clineMessages
	if err := json.Unmarshal(b, &msgs); err != nil {
		diagMalformedLine(path)
		return nil, nil
	}
	// Only the lead agent's transcript is a user-facing session; subagent
	// and teammate message files share the format but not the meaning.
	if msgs.Agent != "" && msgs.Agent != "lead" {
		return nil, nil
	}
	sessionDir := filepath.Dir(path)
	id := filepath.Base(sessionDir)
	s := model.Session{Harness: "cline", ID: id, Path: path, Project: "cline"}
	var man clineManifest
	if mb, err := os.ReadFile(filepath.Join(sessionDir, id+".json")); err == nil {
		if json.Unmarshal(mb, &man) == nil {
			cwd := man.CWD
			if cwd == "" {
				cwd = man.WorkspaceRoot
			}
			if cwd != "" {
				s.Project = claudeProjectName(strings.ReplaceAll(cwd, "/", "-"))
			}
			s.Title = strings.TrimSpace(man.Metadata.Title)
			if s.Title == "" {
				s.Title = firstLineTrim(man.Prompt)
			}
			// The spec'd manifest uses created_at/updated_at; the shipped
			// CLI (3.0.46, observed live) writes started_at/ended_at.
			if t := parseTimeAny(firstNonEmpty(man.CreatedAt, man.StartedAt)); !t.IsZero() {
				s.Started = t
			}
			if t := parseTimeAny(firstNonEmpty(man.UpdatedAt, man.EndedAt)); !t.IsZero() {
				s.Updated = t
			}
		}
	}
	for _, m := range msgs.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		text := clineContentText(m.Content)
		if m.Role == "user" {
			text = unwrapClineTask(text)
		}
		if text == "" {
			continue
		}
		ts := s.Started
		if m.TS > 0 {
			ts = time.UnixMilli(m.TS)
		}
		s.Touch(ts)
		s.Messages = append(s.Messages, model.Message{Role: m.Role, Text: text, Time: ts})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// --- legacy VS Code extension store ---

type clineTaskMeta struct {
	ID   string `json:"id"`
	TS   int64  `json:"ts"`
	Task string `json:"task"`
	CWD  string `json:"cwdOnTaskInitialization"`
}

func parseClineLegacyTask(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var turns []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &turns); err != nil {
		diagMalformedLine(path)
		return nil, nil
	}
	taskDir := filepath.Dir(path)
	taskID := filepath.Base(taskDir)
	root := filepath.Dir(filepath.Dir(taskDir))
	s := model.Session{Harness: "cline", ID: "cline-task-" + taskID, Path: path, Project: "cline"}
	base := time.Time{}
	if hb, err := os.ReadFile(filepath.Join(root, "state", "taskHistory.json")); err == nil {
		var metas []clineTaskMeta
		if json.Unmarshal(hb, &metas) == nil {
			for _, m := range metas {
				if m.ID == taskID {
					s.Title = firstLineTrim(m.Task)
					if m.CWD != "" {
						s.Project = claudeProjectName(strings.ReplaceAll(m.CWD, "/", "-"))
					}
					if m.TS > 0 {
						base = time.UnixMilli(m.TS)
					}
					break
				}
			}
		}
	}
	if base.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			base = fi.ModTime()
		}
	}
	for ti, m := range turns {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		text := clineContentText(m.Content)
		if m.Role == "user" {
			text = unwrapClineTask(text)
		}
		if text == "" {
			continue
		}
		ts := base.Add(time.Duration(ti) * time.Second)
		s.Touch(ts)
		s.Messages = append(s.Messages, model.Message{Role: m.Role, Text: text, Time: ts})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// clineContentText extracts plain text from either a string content or a
// typed block list, keeping only type:"text" blocks.
func clineContentText(raw json.RawMessage) string {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, blk := range blocks {
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			parts = append(parts, strings.TrimSpace(blk.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// unwrapClineTask strips the legacy <task>...</task> envelope (and its modern
// user-input equivalent) so the tags themselves are not indexed.
func unwrapClineTask(text string) string {
	t := strings.TrimSpace(text)
	for _, tag := range []string{"task", "user_message", "user_input"} {
		open := "<" + tag
		if !strings.HasPrefix(t, open) {
			continue
		}
		rest := t[len(open):]
		// The live CLI writes attributes: <user_input mode="act">.
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			return t
		}
		rest = rest[gt+1:]
		if i := strings.Index(rest, "</"+tag+">"); i >= 0 {
			rest = rest[:i] + rest[i+len("</"+tag+">"):]
		}
		return strings.TrimSpace(rest)
	}
	return t
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstLineTrim(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
