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
	if filepath.Base(p) != codeartsHistoryFile {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(CodeArtsRoot()), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ""
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

	exits := commandExits{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		codeartsLineInto(&s, m, exits)
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
func codeartsLineInto(s *model.Session, m map[string]any, exits commandExits) {
	role, blocks, results, speeches, t := codeartsDecodeParts(m)
	if len(speeches) == 0 && len(blocks) == 0 && len(results) == 0 {
		return
	}
	if role == "assistant" {
		if id := str(m["model"]); id != "" {
			s.SetModel(str(m["provider"]), id)
		}
		for _, k := range []string{"usage", "tokens", "token_usage"} {
			if u, ok := m[k].(map[string]any); ok && len(u) > 0 {
				applyUsageMap(s, u)
				break
			}
		}
	}
	for _, it := range blocks {
		b, ok := it.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "tool_use":
			if name := str(b["name"]); name != "" {
				s.AddToolCall(name, false)
			}
		case "tool_result":
			isErr, _ := b["is_error"].(bool)
			code, hasCode := flatRoleResultExitCode(b)
			if isErr || (hasCode && code != 0) {
				s.ToolErrors++
			}
		}
	}
	if !t.IsZero() {
		s.Touch(t)
	}
	for _, txt := range speeches {
		s.Messages = append(s.Messages, model.Message{Role: role, Text: capParsedMessage(txt), Time: t})
	}
	from := len(s.Messages)
	s.Messages = append(s.Messages, dialectWorkRecords(blocks, results, flatRoleDialect, t)...)
	if exits != nil {
		joinResultExits(s.Messages, from, blocks, flatRoleDialect, exits, flatRoleResultExitCode)
	}
}

func codeartsDecodeParts(m map[string]any) (role string, blocks []any, results []string, speeches []string, t time.Time) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", nil, nil, nil, time.Time{}
	}
	var line codeartsLine
	if json.Unmarshal(raw, &line) != nil {
		return "", nil, nil, nil, time.Time{}
	}
	role = strings.ToLower(line.Role)
	t = codeartsTime(line.Timestamp)
	isSpeechRole := role == "user" || role == "assistant"
	for _, p := range codeartsParts(line.Content) {
		if p.Text != nil && p.Text.Text != "" {
			if isSpeechRole {
				b, r, sp, _ := unpackContentBlocks(p.Text.Text)
				blocks = append(blocks, b...)
				results = append(results, r...)
				if sp != "" {
					speeches = append(speeches, sp)
				}
			} else {
				results = append(results, p.Text.Text)
			}
		}
		if callBlock, ok := codeartsDecodeCall(p.ToolCall); ok {
			blocks = append(blocks, callBlock)
		}
		if resBlock, body, ok := codeartsDecodeResult(p.ToolResult); ok {
			blocks = append(blocks, resBlock)
			if body != "" {
				results = append(results, body)
			}
		}
	}
	return role, blocks, results, speeches, t
}

func codeartsDecodeCall(v any) (map[string]any, bool) {
	cm, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	name, _ := cm["name"].(string)
	if name == "" {
		return nil, false
	}
	id := firstNonEmptyStr(str(cm["id"]), str(cm["tool_call_id"]), str(cm["tool_use_id"]))
	in := codeartsArgMap(cm["input"])
	if in == nil {
		in = codeartsArgMap(cm["arguments"])
	}
	if in == nil {
		in = codeartsArgMap(cm["parameters"])
	}
	out := map[string]any{
		"type": "tool_use",
		"name": name,
	}
	if id != "" {
		out["id"] = id
	}
	if in != nil {
		out["input"] = in
	}
	return out, true
}

func codeartsArgMap(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(x)), &m) == nil {
			return m
		}
	}
	return nil
}

func codeartsDecodeResult(v any) (map[string]any, string, bool) {
	rm, ok := v.(map[string]any)
	if !ok {
		return nil, "", false
	}
	id := firstNonEmptyStr(str(rm["tool_call_id"]), str(rm["tool_use_id"]), str(rm["id"]))
	body := codeartsResultBody(rm["result"])
	if body == "" {
		body = firstNonEmptyStr(contentText(rm["content"]), str(rm["output"]), str(rm["tool_output"]), str(rm["error"]))
	}
	tb := map[string]any{
		"type":        "tool_result",
		"tool_use_id": id,
		"content":     body,
	}
	if bad, _ := rm["is_error"].(bool); bad {
		tb["is_error"] = true
	}
	if code, ok := anyInt(rm["exit_code"]); ok {
		tb["exit_code"] = code
	} else if code, ok := anyInt(rm["exitCode"]); ok {
		tb["exit_code"] = code
	} else if resMap, ok := rm["result"].(map[string]any); ok {
		if code, ok := anyInt(resMap["exit_code"]); ok {
			tb["exit_code"] = code
		} else if code, ok := anyInt(resMap["exitCode"]); ok {
			tb["exit_code"] = code
		}
		if errStr, _ := resMap["error"].(string); errStr != "" {
			tb["is_error"] = true
		}
	}
	return tb, body, true
}

func codeartsResultBody(v any) string {
	switch r := v.(type) {
	case string:
		return r
	case map[string]any:
		for _, k := range []string{"tool_output", "output", "content", "text", "error"} {
			if s := contentText(r[k]); s != "" {
				return s
			}
			if s, _ := r[k].(string); s != "" {
				return s
			}
		}
		stdout, _ := r["stdout"].(string)
		stderr, _ := r["stderr"].(string)
		switch {
		case stdout != "" && stderr != "":
			return stdout + "\n" + stderr
		case stdout != "":
			return stdout
		default:
			return stderr
		}
	}
	return ""
}

var codeartsResumes = resumesUnlessSettling(`"Tool`, func(m map[string]any) (calls, failed, clean []string) {
	_, blocks, _, _, _ := codeartsDecodeParts(m)
	for _, it := range blocks {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "tool_use":
			if id := str(p["id"]); id != "" {
				calls = append(calls, id)
			}
		case "tool_result":
			id := str(p["tool_use_id"])
			if id == "" {
				continue
			}
			code, hasCode := flatRoleResultExitCode(p)
			if id == inlineToolID || (hasCode && code != 0) {
				failed = append(failed, id)
			} else {
				clean = append(clean, id)
			}
		}
	}
	return calls, failed, clean
}, func(line []byte) []string {
	m := decodeJSONLine(line)
	if m == nil {
		return nil
	}
	_, blocks, _, _, _ := codeartsDecodeParts(m)
	var ids []string
	for _, c := range commandCallsIn(blocks, flatRoleDialect) {
		ids = append(ids, c.ID)
	}
	return ids
})

// LoadCodeArts is the cold load for the registry.
func LoadCodeArts() []model.Session {
	return parseFiles(CodeArtsFiles(), ParseCodeArtsFile)
}
