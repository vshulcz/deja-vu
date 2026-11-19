package sources

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Claw Code (ultraworkers/claw-code) stores sessions under
// ${CLAW_CONFIG_HOME:-~/.claw}/sessions/<workspace_hash>/<session_id>.{jsonl,json}.
// Current builds append JSONL records (session_meta, compaction, prompt_history,
// message with role/blocks); older snapshots are single JSON documents holding a
// top-level messages array. Tool calls store input as a JSON-encoded string, and
// bash results wrap stdout/stderr/returnCodeInterpretation inside ToolResult.output.

// ClawCodeConfigDir returns Claw Code's configuration root ($CLAW_CONFIG_HOME or ~/.claw).
func ClawCodeConfigDir() string {
	if d := os.Getenv("CLAW_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".claw")
}

// ClawCodeRoot returns the primary session store directory. DEJA_CLAWCODE_ROOT overrides it.
func ClawCodeRoot() string {
	return EnvPath("DEJA_CLAWCODE_ROOT", filepath.Join(ClawCodeConfigDir(), "sessions"))
}

// ClawCodeRoots returns every Claw Code session store directory to index.
func ClawCodeRoots() []string {
	if os.Getenv("DEJA_CLAWCODE_ROOT") != "" {
		return []string{ClawCodeRoot()}
	}
	out := []string{ClawCodeRoot()}
	seen := map[string]bool{filepath.Clean(out[0]): true}
	addDir := func(p string) {
		p = filepath.Clean(p)
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			seen[p] = true
			out = append(out, p)
		}
	}
	if os.Getenv("CLAW_CONFIG_HOME") == "" {
		for _, home := range extraHomeRoots() {
			addDir(filepath.Join(home, ".claw", "sessions"))
		}
	}
	if list := os.Getenv("DEJA_CLAWCODE_ROOTS"); list != "" {
		for _, r := range filepath.SplitList(list) {
			if r == "" {
				continue
			}
			cand := filepath.Join(r, ".claw", "sessions")
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				addDir(cand)
			} else {
				addDir(r)
			}
		}
	}
	return out
}

func isClawCodeSessionFile(p string) bool {
	if strings.HasSuffix(p, ".meta.json") || strings.HasSuffix(p, ".deleted.json") {
		return false
	}
	return strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".json")
}

// ClawCodeSessionFiles lists every Claw Code session transcript across known roots.
func ClawCodeSessionFiles() []string {
	roots := ClawCodeRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, walkFiles(root, isClawCodeSessionFile)...)
	}
	return out
}

// ClawCodeUnderRoot reports whether p is a Claw Code session file under a known store root.
func ClawCodeUnderRoot(p string) bool {
	if !isClawCodeSessionFile(p) {
		return false
	}
	cleanP := filepath.Clean(p)
	for _, r := range ClawCodeRoots() {
		cleanR := filepath.Clean(r)
		if cleanR != "" && strings.HasPrefix(cleanP, cleanR+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func cleanClawCodeWorkspace(ws string) string {
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return ""
	}
	if filepath.IsAbs(ws) {
		return filepath.Clean(ws)
	}
	if strings.HasPrefix(ws, "/") {
		return path.Clean(ws)
	}
	return ""
}

// ClawCodeSessionDir returns the workspace_root recorded in a Claw Code session file.
func ClawCodeSessionDir(p string) string {
	if strings.HasSuffix(p, ".json") {
		b, err := readJSONFile(p)
		if err != nil {
			return ""
		}
		var doc struct {
			WorkspaceRoot string `json:"workspace_root"`
		}
		if json.Unmarshal(b, &doc) == nil {
			return cleanClawCodeWorkspace(doc.WorkspaceRoot)
		}
		return ""
	}
	var dir string
	_ = scanJSONLFromOffset(p, 0, func(m map[string]any) {
		if dir != "" {
			return
		}
		if ws, _ := m["workspace_root"].(string); ws != "" {
			dir = cleanClawCodeWorkspace(ws)
		}
	})
	return dir
}

// LoadClawCode reads every Claw Code session on this machine.
func LoadClawCode() []model.Session {
	return parseFiles(ClawCodeSessionFiles(), ParseClawCodeFile)
}

var clawCodeDialect = toolDialect{
	pathKey:    "path",
	pathTools:  map[string]bool{"read_file": true, "write_file": true, "edit_file": true},
	shellTool:  "bash",
	editTools:  map[string]bool{"write_file": true, "edit_file": true},
	oldKey:     "old_string",
	newKey:     "new_string",
	contentKey: "content",
	commandKey: "command",
}

// ParseClawCodeFile parses one Claw Code .jsonl or legacy .json session file.
func ParseClawCodeFile(path string) ([]model.Session, error) {
	return ParseClawCodeFileFromOffset(path, 0)
}

// ParseClawCodeFileFromOffset parses a Claw Code session incrementally from offset.
func ParseClawCodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	if strings.HasSuffix(path, ".json") {
		return parseClawCodeLegacyJSON(path)
	}
	return parseClawCodeJSONL(path, offset)
}

func parseClawCodeLegacyJSON(path string) ([]model.Session, error) {
	b, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	b = sanitizeClawBytes(b)
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var doc map[string]any
	if err := d.Decode(&doc); err != nil || doc == nil {
		return nil, err
	}
	rawMsgs, ok := doc["messages"].([]any)
	if !ok {
		return nil, nil
	}

	s := newClawCodeSession(path)
	curTS := applyClawMeta(&s, doc)
	exits := commandExits{}
	for _, it := range rawMsgs {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		appendClawMessage(&s, m, curTS, exits)
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	sanitizeClawSession(&s)
	return []model.Session{s}, nil
}

func parseClawCodeJSONL(path string, offset int64) ([]model.Session, error) {
	s := newClawCodeSession(path)
	var curTS time.Time
	if offset > 0 {
		curTS = clawTimeBefore(path, offset)
	}
	exits := commandExits{}
	err := scanJSONLWithHeaderFromOffsetFunc(path, offset, headerLookahead, isClawMeta, func(m map[string]any) {
		kind, _ := m["type"].(string)
		switch kind {
		case "session_meta":
			ts := applyClawMeta(&s, m)
			if curTS.IsZero() && !ts.IsZero() {
				curTS = ts
			}
		case "prompt_history":
			// render_jsonl_snapshot writes prompt_history ahead of messages; keep only
			// its clock advance so user prompts are never indexed twice.
			if ts := clawCodeTime(m["timestamp_ms"]); !ts.IsZero() {
				curTS = ts
			}
		case "message":
			if mod, _ := m["model"].(string); mod != "" {
				s.SetModel(str(m["provider"]), mod)
			}
			msgMap, _ := m["message"].(map[string]any)
			if msgMap == nil {
				msgMap = m
			} else {
				applyUsageMap(&s, m)
			}
			appendClawMessage(&s, msgMap, curTS, exits)
		default:
			if _, ok := m["role"].(string); ok {
				appendClawMessage(&s, m, curTS, exits)
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	sanitizeClawSession(&s)
	return []model.Session{s}, err
}

func newClawCodeSession(path string) model.Session {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".jsonl")
	base = strings.TrimSuffix(base, ".json")
	return model.Session{
		Harness: "clawcode",
		ID:      base,
		Path:    path,
	}
}

func isClawMeta(m map[string]any) bool {
	t, _ := m["type"].(string)
	return t == "session_meta"
}

func applyClawMeta(s *model.Session, m map[string]any) time.Time {
	if id, _ := m["session_id"].(string); id != "" {
		s.ID = id
	}
	if mod, _ := m["model"].(string); mod != "" {
		s.SetModel(str(m["provider"]), mod)
	}
	applyUsageMap(s, m)
	if ws, _ := m["workspace_root"].(string); ws != "" {
		s.Project = projectName(ws)
	}
	if fork, ok := m["fork"].(map[string]any); ok {
		if parent, _ := fork["parent_session_id"].(string); parent != "" {
			s.Parent = parent
		}
	}
	created := clawCodeTime(m["created_at_ms"])
	if !created.IsZero() {
		s.Touch(created)
	}
	updated := clawCodeTime(m["updated_at_ms"])
	if !updated.IsZero() {
		s.Touch(updated)
	}
	if !created.IsZero() {
		return created
	}
	return updated
}

func clawTimeBefore(path string, offset int64) time.Time {
	var at time.Time
	if offset <= 0 {
		return at
	}
	eachLineBefore(path, offset, func(line []byte) bool {
		m := decodeJSONLine(line)
		switch m["type"] {
		case "prompt_history":
			at = clawCodeTime(m["timestamp_ms"])
		case "session_meta":
			at = clawCodeTime(m["created_at_ms"])
			if at.IsZero() {
				at = clawCodeTime(m["updated_at_ms"])
			}
		}
		return at.IsZero()
	})
	return at
}

func appendClawMessage(s *model.Session, msg map[string]any, ts time.Time, exits commandExits) {
	role, _ := msg["role"].(string)
	blocks := normalizeClawBlocks(msg["blocks"])
	if blocks == nil {
		blocks = normalizeClawBlocks(msg["content"])
	}
	text := clawCodeText(blocks, msg["content"])
	start := len(s.Messages)

	switch role {
	case "user":
		if text != "" {
			s.Touch(ts)
			if s.Title == "" {
				s.Title = capParsedMessage(text)
			}
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: ts})
		}
	case "assistant":
		if id := str(msg["model"]); id != "" {
			s.SetModel(str(msg["provider"]), id)
		}
		for _, k := range []string{"usage", "tokens", "token_usage"} {
			if u, ok := msg[k].(map[string]any); ok && len(u) > 0 {
				applyUsageMap(s, u)
				break
			}
		}
		if text != "" {
			s.Touch(ts)
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: ts})
		}
		if work := dialectWorkRecords(blocks, nil, clawCodeDialect, ts); len(work) > 0 {
			s.Touch(ts)
			s.Messages = append(s.Messages, work...)
		}
	case "tool":
		// Tool results attach below via joinResultExits and RoleToolOutput.
	default:
		return
	}

	if len(blocks) > 0 {
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
				code, hasCode := clawCodeResultStatus(b)
				isErr, _ := b["is_error"].(bool)
				if isErr || (hasCode && code != 0) {
					s.ToolErrors++
				}
			}
		}
		joinResultExits(s.Messages, start, blocks, clawCodeDialect, exits, clawCodeExitCode)
		if IndexToolOutput() {
			for _, it := range blocks {
				b, ok := it.(map[string]any)
				if !ok || b["type"] != "tool_result" {
					continue
				}
				if out := clawCodeToolOutputText(b); out != "" {
					s.Touch(ts)
					s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: ts})
				}
			}
		}
	}
}

func normalizeClawBlocks(raw any) []any {
	blocks, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(blocks))
	for _, it := range blocks {
		b, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if b["type"] == "tool_use" {
			if inStr, ok := b["input"].(string); ok {
				var parsed map[string]any
				d := json.NewDecoder(strings.NewReader(inStr))
				d.UseNumber()
				if d.Decode(&parsed) == nil && parsed != nil {
					cp := maps.Clone(b)
					cp["input"] = parsed
					out = append(out, cp)
					continue
				}
			}
		}
		out = append(out, b)
	}
	return out
}

func clawCodeText(blocks []any, fallback any) string {
	if s, ok := fallback.(string); ok && s != "" && len(blocks) == 0 {
		return s
	}
	var b strings.Builder
	for _, it := range blocks {
		m, ok := it.(map[string]any)
		if !ok || m["type"] != "text" {
			continue
		}
		txt, _ := m["text"].(string)
		if txt == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(txt)
	}
	return b.String()
}

func clawCodeRawOutput(result map[string]any) string {
	if s, ok := result["output"].(string); ok {
		return s
	}
	if s, ok := result["content"].(string); ok {
		return s
	}
	return textFromContent(result["content"])
}

func clawCodeToolOutputText(result map[string]any) string {
	raw := clawCodeRawOutput(result)
	if raw == "" {
		return ""
	}
	if obj := decodeJSONObject(raw); obj != nil {
		if _, hasStdout := obj["stdout"]; hasStdout {
			stdout, _ := obj["stdout"].(string)
			stderr, _ := obj["stderr"].(string)
			var parts []string
			if stdout != "" {
				parts = append(parts, strings.TrimRight(stdout, "\n"))
			}
			if stderr != "" {
				parts = append(parts, strings.TrimRight(stderr, "\n"))
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
			if msg, _ := obj["returnCodeInterpretation"].(string); msg != "" {
				return msg
			}
			return ""
		}
	}
	return raw
}

func clawCodeResultStatus(result map[string]any) (int, bool) {
	if tn, _ := result["tool_name"].(string); tn != "" && tn != "bash" && tn != "Bash" {
		return 0, false
	}
	raw := clawCodeRawOutput(result)
	isErr, _ := result["is_error"].(bool)
	if obj := decodeJSONObject(raw); obj != nil {
		if interp, _ := obj["returnCodeInterpretation"].(string); interp != "" {
			if n, ok := statusCode(interp, "exit_code:", ""); ok {
				return n, true
			}
			return 0, false
		}
		if interrupted, _ := obj["interrupted"].(bool); interrupted {
			return 0, false
		}
		_, hasStdout := obj["stdout"]
		_, hasStderr := obj["stderr"]
		if (hasStdout || hasStderr) && !isErr {
			return 0, true
		}
		return 0, false
	}
	first, _, _ := strings.Cut(raw, "\n")
	if isErr {
		for _, p := range []struct{ prefix, suffix string }{
			{"exit_code:", ""},
			{"Exit code: ", " ("},
			{"Exit code ", ""},
		} {
			if n, ok := statusCode(first, p.prefix, p.suffix); ok {
				return n, true
			}
		}
		if n, ok := statusCode(lastLine(raw), "Command exited with code ", ""); ok {
			return n, true
		}
		return 0, false
	}
	for _, p := range []struct{ prefix, suffix string }{
		{"Exit code: ", " ("},
		{"Exit code ", ""},
	} {
		if n, ok := statusCode(first, p.prefix, p.suffix); ok && n == 0 {
			return 0, true
		}
	}
	return 0, false
}

// clawCodeExitCode reports an explicit command exit status (including 0 when
// the result carries one cleanly) while leaving ambiguous runs unannotated.
func clawCodeExitCode(result map[string]any) (int, bool) {
	return clawCodeResultStatus(result)
}

func decodeJSONObject(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(trimmed), &obj) != nil {
		return nil
	}
	return obj
}

func clawCodeTime(v any) time.Time {
	if t := parseTimeAny(v); !t.IsZero() {
		return t
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil && i > 0 {
			if i >= 1e12 {
				return time.UnixMilli(i).UTC()
			}
			return time.Unix(i, 0).UTC()
		}
	}
	return time.Time{}
}

var clawCodeResumes = resumesUnlessSettling(`"tool_`, func(m map[string]any) (calls, failed, clean []string) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		msg = m
	}
	blocks := normalizeClawBlocks(msg["blocks"])
	if blocks == nil {
		blocks = normalizeClawBlocks(msg["content"])
	}
	for _, it := range blocks {
		b, ok := it.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "tool_use":
			if id, _ := b["id"].(string); id != "" {
				calls = append(calls, id)
			}
		case "tool_result":
			id, _ := b["tool_use_id"].(string)
			if id == "" {
				continue
			}
			if code, ok := clawCodeResultStatus(b); ok {
				if code != 0 {
					failed = append(failed, id)
				} else {
					clean = append(clean, id)
				}
			}
		}
	}
	return calls, failed, clean
}, func(line []byte) []string {
	m := decodeJSONLine(line)
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		msg = m
	}
	blocks := normalizeClawBlocks(msg["blocks"])
	if blocks == nil {
		blocks = normalizeClawBlocks(msg["content"])
	}
	cmds := commandCallsIn(blocks, clawCodeDialect)
	ids := make([]string, 0, len(cmds))
	for _, c := range cmds {
		ids = append(ids, c.ID)
	}
	return ids
})

func cleanClawString(s string) string {
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, '\x00') {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if utf8.ValidString(s) {
		return s
	}
	return string(validUTF8([]byte(s)))
}

func sanitizeClawBytes(b []byte) []byte {
	if bytes.IndexByte(b, '\x00') >= 0 {
		b = bytes.ReplaceAll(b, []byte{0}, nil)
	}
	return validUTF8(b)
}

func sanitizeClawSession(s *model.Session) {
	s.ID = cleanClawString(s.ID)
	s.Project = cleanClawString(s.Project)
	s.Title = cleanClawString(s.Title)
	s.Parent = cleanClawString(s.Parent)
	for i := range s.Messages {
		s.Messages[i].Text = cleanClawString(s.Messages[i].Text)
	}
}
