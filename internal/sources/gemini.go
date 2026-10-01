package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Gemini CLI records chats under ~/.gemini/tmp/<projectId>/chats/ in two
// generations: whole-session JSON files and newer JSONL logs where the first
// line is metadata and later lines are messages, "$set" metadata patches, or
// "$rewindTo" truncation markers. Project ids are either path-hashes or
// slugs; ~/.gemini/projects.json maps real paths to ids.

// GeminiHome honors GEMINI_CLI_HOME, the home-dir override Gemini CLI itself
// implements (it appends .gemini). The often-cited GEMINI_CONFIG_DIR only
// exists in upstream feature requests and is deliberately not read.
func GeminiHome() string {
	if h := os.Getenv("GEMINI_CLI_HOME"); h != "" {
		return filepath.Join(h, ".gemini")
	}
	return filepath.Join(Home(), ".gemini")
}

// GeminiRoot is the session-reading root; DEJA_GEMINI_ROOT overrides it
// without affecting where install writes.
func GeminiRoot() string {
	return EnvPath("DEJA_GEMINI_ROOT", GeminiHome())
}

// GeminiSidecarFiles lists what a Gemini store keeps under `tmp` beside the
// chats: the per-project log the CLI writes for itself. Everything outside
// `tmp` is out of the walk entirely, because Antigravity's store lives in a
// sibling directory of the same root and has its own row (#3397).
func GeminiSidecarFiles() []string {
	return walkFiles(filepath.Join(GeminiRoot(), "tmp"), func(p string) bool {
		return filepath.Base(p) == "logs.json"
	})
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
		walked, under := walkRoot(chats)
		_ = filepath.WalkDir(walked, func(p string, d os.DirEntry, err error) error {
			// Regular files only: a FIFO matching the glob would block the
			// parser's Open forever (same hang walkFiles guards against).
			if err == nil && d.Type().IsRegular() && (strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonl")) {
				out = append(out, under(p))
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
	// ToolCalls is what the model ran: id, name, args and the result the
	// tool answered with. The reply also arrives as the next user record,
	// content [{functionResponse}], which is where the output is read from
	// so it is indexed once (#3293).
	ToolCalls json.RawMessage `json:"toolCalls"`
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
			// Newer Gemini CLI builds write the message state inside $set
			// snapshots (sometimes the only message-bearing lines in the
			// file). A $set replaces the state collected so far.
			if list, ok := patch["messages"].([]any); ok {
				var snap []geminiMessage
				for _, item := range list {
					raw, err := json.Marshal(item)
					if err != nil {
						continue
					}
					var gm geminiMessage
					if json.Unmarshal(raw, &gm) == nil && gm.Type != "" {
						snap = append(snap, gm)
					}
				}
				if len(snap) > 0 || len(list) == 0 {
					msgs = snap
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
	// A shell call's command record, by call id, so the exit status its
	// result reports can ride on it.
	shellAt := map[string]int{}
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
		t, _ := time.Parse(time.RFC3339Nano, m.Timestamp)
		if t.IsZero() {
			t = s.Started
		}
		// The work rides on the same records as the talk: a gemini record's
		// toolCalls name the command and the file, a user record made of
		// functionResponse parts carries what came back. Read as text only,
		// a Gemini store yielded no command, no tool output and no fix pair
		// (#3293). Qwen's dialect follows Gemini's tool names, so the same
		// reader serves both.
		if work := geminiWorkRecords(m, t); len(work) > 0 {
			s.Touch(t)
			start := len(s.Messages)
			s.Messages = append(s.Messages, work...)
			geminiNoteShellCalls(s, m, start, shellAt)
		}
		geminiNoteExits(s, m, shellAt)
		text := geminiContentText(m.Content)
		if text == "" {
			continue
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	}
}

// geminiExit is the status run_shell_command reports in its result:
// "Exit Code: 128". Codex, opencode and Cursor commands carry `→ exit N` on a
// non-zero exit, which is what the failed-command recall reads; a Gemini
// failure read like a success without it (#4208).
var geminiExit = regexp.MustCompile(`(?m)^Exit Code: (\d+)`)

type geminiCall struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Args   map[string]any `json:"args"`
	Result []struct {
		FunctionResponse *geminiResponse `json:"functionResponse"`
	} `json:"result"`
}

type geminiResponse struct {
	ID       string `json:"id"`
	Response struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	} `json:"response"`
}

// geminiNoteShellCalls maps each shell call in m to the command record it
// produced among s.Messages[start:].
func geminiNoteShellCalls(s *model.Session, m geminiMessage, start int, shellAt map[string]int) {
	var calls []geminiCall
	if len(m.ToolCalls) == 0 || json.Unmarshal(m.ToolCalls, &calls) != nil {
		return
	}
	used := map[int]bool{}
	for _, c := range calls {
		cmd, _ := c.Args["command"].(string)
		if c.ID == "" || !qwenDialect.isShellTool(c.Name) || cmd == "" {
			continue
		}
		for i := start; i < len(s.Messages); i++ {
			if !used[i] && s.Messages[i].Role == RoleCommand && s.Messages[i].Text == "$ "+cmd {
				shellAt[c.ID], used[i] = i, true
				break
			}
		}
	}
}

// geminiNoteExits appends a non-zero exit to the command it belongs to, from
// the call's own result or from the functionResponse record that follows.
func geminiNoteExits(s *model.Session, m geminiMessage, shellAt map[string]int) {
	var resps []*geminiResponse
	var calls []geminiCall
	if len(m.ToolCalls) > 0 && json.Unmarshal(m.ToolCalls, &calls) == nil {
		for _, c := range calls {
			for _, r := range c.Result {
				resps = append(resps, r.FunctionResponse)
			}
		}
	}
	var blocks []struct {
		FunctionResponse *geminiResponse `json:"functionResponse"`
	}
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, b := range blocks {
			resps = append(resps, b.FunctionResponse)
		}
	}
	for _, r := range resps {
		if r == nil {
			continue
		}
		i, ok := shellAt[r.ID]
		if !ok {
			continue
		}
		mm := geminiExit.FindStringSubmatch(r.Response.Output + "\n" + r.Response.Error)
		if mm == nil {
			continue
		}
		if code, err := strconv.Atoi(mm[1]); err == nil && code > 0 {
			s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
		}
		delete(shellAt, r.ID) // once: the result arrives on both records
	}
}

// geminiWorkRecords turns a record's toolCalls (the calls, without their
// results) and its functionResponse parts (the results) into work records.
func geminiWorkRecords(m geminiMessage, t time.Time) []model.Message {
	var parts []any
	if len(m.ToolCalls) > 0 {
		var calls []struct {
			Name string         `json:"name"`
			Args map[string]any `json:"args"`
		}
		if json.Unmarshal(m.ToolCalls, &calls) == nil {
			for _, c := range calls {
				if c.Name != "" && c.Args != nil {
					parts = append(parts, map[string]any{"functionCall": map[string]any{"name": c.Name, "args": c.Args}})
				}
			}
		}
	}
	var blocks []map[string]any
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, p := range blocks {
			if resp, ok := p["functionResponse"]; ok {
				parts = append(parts, map[string]any{"functionResponse": resp})
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return qwenWorkRecords(parts, t)
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
