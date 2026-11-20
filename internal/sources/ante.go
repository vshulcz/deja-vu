package sources

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Ante (AntigmaLabs/ante) stores sessions under ${ANTE_HOME:-~/.ante}/sessions/<session_id>/
// as events.jsonl (live event stream) beside meta.json and an optional dialog.json snapshot,
// with spawned subagents nested under <session_id>/subagents/<subagent_id>/.

// AnteConfigDir returns Ante's configuration root ($ANTE_HOME or ~/.ante).
func AnteConfigDir() string {
	if d := strings.TrimSpace(os.Getenv("ANTE_HOME")); d != "" {
		return filepath.Clean(d)
	}
	return filepath.Join(Home(), ".ante")
}

// AnteRoot returns the primary session store directory. DEJA_ANTE_ROOT overrides it.
func AnteRoot() string {
	return EnvPath("DEJA_ANTE_ROOT", filepath.Join(AnteConfigDir(), "sessions"))
}

// AnteRoots returns every Ante session store directory to index.
func AnteRoots() []string {
	if os.Getenv("DEJA_ANTE_ROOT") != "" {
		return []string{AnteRoot()}
	}
	out := []string{AnteRoot()}
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
	if strings.TrimSpace(os.Getenv("ANTE_HOME")) == "" {
		for _, home := range extraHomeRoots() {
			addDir(filepath.Join(home, ".ante", "sessions"))
		}
	}
	if list := os.Getenv("DEJA_ANTE_ROOTS"); list != "" {
		for _, r := range filepath.SplitList(list) {
			if r == "" {
				continue
			}
			cand := filepath.Join(r, ".ante", "sessions")
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				addDir(cand)
			} else {
				addDir(r)
			}
		}
	}
	return out
}

// AnteSubagentFile reports whether p sits inside an Ante session's subagents directory.
func AnteSubagentFile(p string) bool {
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Clean(p)))) == "subagents"
}

func anteWantFile(p string) bool {
	if SubagentsOff() && AnteSubagentFile(p) {
		return false
	}
	base := filepath.Base(p)
	if base == "events.jsonl" {
		return true
	}
	if base == "dialog.json" {
		_, err := os.Stat(filepath.Join(filepath.Dir(p), "events.jsonl"))
		return err != nil
	}
	return false
}

// AnteUnderRoot reports whether p is a primary Ante transcript under a known store root.
func AnteUnderRoot(p string) bool {
	base := filepath.Base(p)
	if base != "events.jsonl" && base != "dialog.json" {
		return false
	}
	if base == "dialog.json" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(p), "events.jsonl")); err == nil {
			return false
		}
	}
	cleanP := filepath.Clean(p)
	for _, r := range AnteRoots() {
		cleanR := filepath.Clean(r)
		if cleanR != "" && strings.HasPrefix(cleanP, cleanR+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// AnteSessionFiles lists every Ante session transcript across known roots,
// preferring events.jsonl and falling back to dialog.json only when events.jsonl is absent.
func AnteSessionFiles() []string {
	roots := AnteRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, walkFiles(root, anteWantFile)...)
	}
	return out
}

// AnteSidecarFiles lists Ante's metadata sidecars (meta.json and dialog.json when
// events.jsonl exists beside it) so doctor does not report them as unread transcripts.
func AnteSidecarFiles() []string {
	roots := AnteRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, walkFiles(root, func(p string) bool {
			base := filepath.Base(p)
			if base == "meta.json" {
				return true
			}
			if base == "dialog.json" {
				_, err := os.Stat(filepath.Join(filepath.Dir(p), "events.jsonl"))
				return err == nil
			}
			return false
		})...)
	}
	return out
}

// AnteSessionDir returns the working directory recorded in a session's sibling meta.json.
func AnteSessionDir(path string) string {
	meta, ok := readAnteMeta(filepath.Join(filepath.Dir(path), "meta.json"))
	if !ok && AnteSubagentFile(path) {
		parentMetaPath := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(path))), "meta.json")
		meta, ok = readAnteMeta(parentMetaPath)
	}
	if !ok {
		return ""
	}
	return meta.workDir()
}

// LoadAnte reads every Ante session on this machine.
func LoadAnte() []model.Session {
	return parseFiles(AnteSessionFiles(), ParseAnteFile)
}

// ParseAnteFile parses one Ante events.jsonl or dialog.json session file.
func ParseAnteFile(path string) ([]model.Session, error) {
	return ParseAnteFileFromOffset(path, 0)
}

// ParseAnteFileFromOffset parses an Ante session incrementally from offset.
func ParseAnteFileFromOffset(path string, offset int64) ([]model.Session, error) {
	if filepath.Base(path) == "dialog.json" {
		return parseAnteDialog(path)
	}
	return parseAnteEvents(path, offset)
}

type anteMeta struct {
	ID               string         `json:"id"`
	Provider         string         `json:"provider"`
	Model            string         `json:"model"`
	Dir              string         `json:"dir"`
	CWD              string         `json:"cwd"`
	FirstUserMessage string         `json:"first_user_message"`
	Usage            map[string]any `json:"usage"`
	StartedTime      any            `json:"started_time"`
	LastActiveTime   any            `json:"last_active_time"`
	ForkedFrom       string         `json:"forked_from"`
}

func (m anteMeta) workDir() string {
	if d := strings.TrimSpace(m.Dir); d != "" {
		return d
	}
	return strings.TrimSpace(m.CWD)
}

func readAnteMeta(path string) (anteMeta, bool) {
	b, err := readJSONFile(path)
	if err != nil {
		return anteMeta{}, false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var meta anteMeta
	if err := d.Decode(&meta); err != nil {
		return anteMeta{}, false
	}
	return meta, true
}

func anteBaseSession(path string) (model.Session, anteMeta, string, bool) {
	sessionDir := filepath.Dir(path)
	child := AnteSubagentFile(path)
	id := filepath.Base(sessionDir)
	s := model.Session{
		Harness: "ante",
		ID:      id,
		Path:    path,
	}
	meta, hasMeta := readAnteMeta(filepath.Join(sessionDir, "meta.json"))
	cwd := ""
	if hasMeta {
		if mid := strings.TrimSpace(meta.ID); mid != "" {
			s.ID = mid
		}
		cwd = meta.workDir()
		s.SetModel(strings.TrimSpace(meta.Provider), strings.TrimSpace(meta.Model))
		if title := anteFirstLine(meta.FirstUserMessage); title != "" && !child {
			s.Title = title
		}
		if !child {
			s.Touch(parseTimeAny(meta.StartedTime))
			s.Touch(parseTimeAny(meta.LastActiveTime))
		}
	}
	if child {
		parentDir := filepath.Dir(filepath.Dir(sessionDir))
		parentID := filepath.Base(parentDir)
		s.Kind = model.KindSubagent
		s.Parent = parentID
		if !hasMeta || strings.TrimSpace(meta.ID) == "" || meta.ID == parentID {
			if strings.HasPrefix(id, "ses_") {
				s.ID = id
			} else {
				s.ID = parentID + "-" + id
			}
		}
		if cwd == "" {
			if pMeta, ok := readAnteMeta(filepath.Join(parentDir, "meta.json")); ok {
				cwd = pMeta.workDir()
			}
		}
	} else if hasMeta {
		if ff := strings.TrimSpace(meta.ForkedFrom); ff != "" && ff != s.ID {
			s.Kind = model.KindFork
			s.Parent = ff
		}
	}
	if cwd != "" {
		s.Project = projectName(cwd)
	}
	return s, meta, cwd, child
}

func anteFirstLine(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

func anteExtractUsageMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if nested, ok := m["usage"].(map[string]any); ok {
		return nested
	}
	return m
}

func anteResolveArgs(args map[string]any, cwd string) map[string]any {
	if args == nil {
		return nil
	}
	p, _ := args[claudeDialect.pathKey].(string)
	if p == "" && cwd != "" {
		return args
	}
	if p != "" {
		if abs := resolveToolPath(p, cwd); abs != p {
			cloned := maps.Clone(args)
			cloned[claudeDialect.pathKey] = abs
			return cloned
		}
	}
	return args
}

func anteUnwrapResult(v any) (map[string]any, string) {
	switch r := v.(type) {
	case map[string]any:
		for _, k := range []string{"Completed", "Error", "Failed"} {
			if inner, ok := r[k].(map[string]any); ok {
				return inner, ""
			}
		}
		return r, ""
	case string:
		return nil, strings.TrimSpace(r)
	default:
		return nil, ""
	}
}

func anteToolOutputText(res map[string]any, fallback string) string {
	if res == nil {
		return fallback
	}
	var parts []string
	for _, key := range []string{"stdout", "stderr", "output"} {
		if v, _ := res[key].(string); strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return fallback
}

func applyAnteSessionStart(s *model.Session, cwd *string, path string, child bool, ss map[string]any) {
	if sid, _ := ss["session_id"].(string); strings.TrimSpace(sid) != "" && !child && s.ID == filepath.Base(filepath.Dir(path)) {
		s.ID = strings.TrimSpace(sid)
	}
	if ff, _ := ss["forked_from"].(string); strings.TrimSpace(ff) != "" && !child && strings.TrimSpace(ff) != s.ID && s.Parent == "" {
		s.Kind = model.KindFork
		s.Parent = strings.TrimSpace(ff)
	}
	if prov, _ := ss["provider"].(string); prov != "" || ss["model"] != nil {
		mod, _ := ss["model"].(string)
		s.SetModel(strings.TrimSpace(prov), strings.TrimSpace(mod))
	}
	if d, _ := ss["dir"].(string); strings.TrimSpace(d) != "" && *cwd == "" {
		*cwd = strings.TrimSpace(d)
		s.Project = projectName(*cwd)
	}
}

func applyAnteShellOutput(s *model.Session, rawSO any, t time.Time) {
	switch so := rawSO.(type) {
	case map[string]any:
		cmd, _ := so["command"].(string)
		cmd = strings.TrimSpace(cmd)
		code, hasCode := anyExitCode(so["exit_code"])
		if cmd != "" {
			s.AddToolCall("Bash", hasCode && code != 0)
			if IndexCommands() {
				msg := model.Message{Role: model.RoleCommand, Text: cmd, Time: t}
				if hasCode && code > 0 {
					msg.Text += "  → exit " + strconv.Itoa(code)
				}
				s.Messages = append(s.Messages, msg)
			}
		} else if hasCode && code != 0 {
			s.ToolErrors++
		}
		if IndexToolOutput() {
			if out := anteToolOutputText(so, ""); out != "" {
				s.Messages = append(s.Messages, model.Message{Role: model.RoleToolOutput, Text: out, Time: t})
			}
		}
	case string:
		if IndexToolOutput() && strings.TrimSpace(so) != "" {
			s.Messages = append(s.Messages, model.Message{Role: model.RoleToolOutput, Text: strings.TrimSpace(so), Time: t})
		}
	}
}

func applyAnteToolStart(s *model.Session, exits *commandExits, cwd string, ts map[string]any, t time.Time) {
	callID, _ := ts["id"].(string)
	name, _ := ts["name"].(string)
	if name != "" {
		s.AddToolCall(name, false)
	}
	args, _ := ts["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	args = anteResolveArgs(args, cwd)
	part := []any{map[string]any{
		"type":  "tool_use",
		"id":    callID,
		"name":  name,
		"input": args,
	}}
	from := len(s.Messages)
	s.Messages = append(s.Messages, dialectWorkRecords(part, nil, claudeDialect, t)...)
	exits.note(s.Messages, from, commandCallsIn(part, claudeDialect))
}

func applyAnteToolEnd(s *model.Session, exits *commandExits, te map[string]any, t time.Time) {
	callID, _ := te["tool_use_id"].(string)
	status, _ := te["status"].(string)
	if status == "Failed" {
		s.ToolErrors++
	}
	res, fallback := anteUnwrapResult(te["result_json"])
	code, hasCode := anyExitCode(nil)
	if res != nil {
		code, hasCode = anyExitCode(res["exit_code"])
	}
	if !hasCode && status == "Failed" {
		code = 1
		hasCode = true
	}
	if hasCode && code != 0 {
		exits.stamp(s.Messages, callID, "", code)
	}
	if IndexToolOutput() {
		if out := anteToolOutputText(res, fallback); out != "" {
			s.Messages = append(s.Messages, model.Message{Role: model.RoleToolOutput, Text: out, Time: t})
		}
	}
}

func parseAnteEvents(path string, offset int64) ([]model.Session, error) {
	s, meta, cwd, child := anteBaseSession(path)
	exits := commandExits{}
	sawStreamUsage := false
	var sessionEndUsage map[string]any

	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		ev, ok := m["event"].(map[string]any)
		if !ok {
			return
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)

		if ss, ok := ev["SessionStart"].(map[string]any); ok {
			applyAnteSessionStart(&s, &cwd, path, child, ss)
		}

		if txt, ok := ev["UserInput"].(string); ok {
			if trimmed := strings.TrimSpace(txt); trimmed != "" {
				s.Messages = append(s.Messages, model.Message{Role: model.RoleUser, Text: trimmed, Time: t})
				if s.Title == "" && !child {
					s.Title = anteFirstLine(trimmed)
				}
			}
		}

		if txt, ok := ev["AgentMessage"].(string); ok {
			if trimmed := strings.TrimSpace(txt); trimmed != "" {
				s.Messages = append(s.Messages, model.Message{Role: model.RoleAssistant, Text: trimmed, Time: t})
			}
		}

		if ce, ok := ev["CompactEnd"].(map[string]any); ok {
			if summary, _ := ce["summary"].(string); strings.TrimSpace(summary) != "" {
				s.Messages = append(s.Messages, model.Message{Role: model.RoleSummary, Text: strings.TrimSpace(summary), Time: t})
			}
		}

		if rawSO, exists := ev["ShellOutput"]; exists {
			applyAnteShellOutput(&s, rawSO, t)
		}

		if ts, ok := ev["ToolStart"].(map[string]any); ok {
			applyAnteToolStart(&s, &exits, cwd, ts, t)
		}

		if te, ok := ev["ToolEnd"].(map[string]any); ok {
			applyAnteToolEnd(&s, &exits, te, t)
		}

		if uu, ok := ev["UsageUpdate"].(map[string]any); ok {
			if u := anteExtractUsageMap(uu); u != nil {
				applyUsageMap(&s, u)
				sawStreamUsage = true
			}
		}

		if se, ok := ev["SessionEnd"].(map[string]any); ok {
			if u, ok := se["usage"].(map[string]any); ok {
				sessionEndUsage = u
			}
		}
	})

	if offset == 0 && !sawStreamUsage {
		if sessionEndUsage != nil {
			applyUsageMap(&s, sessionEndUsage)
		} else if meta.Usage != nil {
			applyUsageMap(&s, meta.Usage)
		}
	}

	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func parseAnteDialog(path string) ([]model.Session, error) {
	b, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var doc map[string]any
	if err := d.Decode(&doc); err != nil || doc == nil {
		return nil, err
	}
	s, meta, cwd, child := anteBaseSession(path)
	if rm, _ := doc["reasoning_model"].(string); strings.TrimSpace(rm) != "" && (s.Model == nil || s.Model.ID == "") {
		s.SetModel("", strings.TrimSpace(rm))
	}
	if meta.Usage != nil {
		applyUsageMap(&s, meta.Usage)
	}
	rawMsgs, ok := doc["messages"].([]any)
	if !ok {
		return nil, nil
	}
	t := s.Updated
	if t.IsZero() {
		t = s.Started
	}
	exits := commandExits{}
	for _, it := range rawMsgs {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "user":
			role = model.RoleUser
		case "model", "assistant":
			role = model.RoleAssistant
		default:
			continue
		}
		content, _ := m["content"].(map[string]any)
		if content == nil {
			continue
		}
		if txt, ok := content["Text"].(string); ok {
			if trimmed := strings.TrimSpace(txt); trimmed != "" {
				s.Messages = append(s.Messages, model.Message{Role: role, Text: trimmed, Time: t})
				if role == model.RoleUser && s.Title == "" && !child {
					s.Title = anteFirstLine(trimmed)
				}
			}
			continue
		}
		parts, _ := content["Parts"].([]any)
		for _, rp := range parts {
			pm, ok := rp.(map[string]any)
			if !ok {
				continue
			}
			if txt, ok := pm["Text"].(string); ok {
				if trimmed := strings.TrimSpace(txt); trimmed != "" {
					s.Messages = append(s.Messages, model.Message{Role: role, Text: trimmed, Time: t})
					if role == model.RoleUser && s.Title == "" && !child {
						s.Title = anteFirstLine(trimmed)
					}
				}
			}
			if tu, ok := pm["ToolUse"].(map[string]any); ok {
				callID, _ := tu["id"].(string)
				name, _ := tu["name"].(string)
				if name != "" {
					s.AddToolCall(name, false)
				}
				args, _ := tu["args"].(map[string]any)
				if args == nil {
					args = map[string]any{}
				}
				args = anteResolveArgs(args, cwd)
				part := []any{map[string]any{
					"type":  "tool_use",
					"id":    callID,
					"name":  name,
					"input": args,
				}}
				from := len(s.Messages)
				s.Messages = append(s.Messages, dialectWorkRecords(part, nil, claudeDialect, t)...)
				exits.note(s.Messages, from, commandCallsIn(part, claudeDialect))
			}
			if tr, ok := pm["ToolResult"].(map[string]any); ok {
				callID, _ := tr["tool_use_id"].(string)
				isErr, _ := tr["is_error"].(bool)
				if isErr {
					s.ToolErrors++
					exits.stamp(s.Messages, callID, "", 1)
				}
				if IndexToolOutput() {
					if out, _ := tr["content"].(string); strings.TrimSpace(out) != "" {
						s.Messages = append(s.Messages, model.Message{Role: model.RoleToolOutput, Text: strings.TrimSpace(out), Time: t})
					}
				}
			}
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

var anteEventsResumes = resumesUnlessSettling(`"Tool`, func(m map[string]any) (calls, failed, clean []string) {
	ev, ok := m["event"].(map[string]any)
	if !ok {
		return nil, nil, nil
	}
	if ts, ok := ev["ToolStart"].(map[string]any); ok {
		if id, _ := ts["id"].(string); id != "" {
			calls = append(calls, id)
		}
	}
	if te, ok := ev["ToolEnd"].(map[string]any); ok {
		id, _ := te["tool_use_id"].(string)
		if id == "" {
			return calls, failed, clean
		}
		status, _ := te["status"].(string)
		res, _ := anteUnwrapResult(te["result_json"])
		code, hasCode := anyExitCode(nil)
		if res != nil {
			code, hasCode = anyExitCode(res["exit_code"])
		}
		if (!hasCode && status == "Failed") || (hasCode && code != 0) {
			failed = append(failed, id)
		} else if status != "Cancelled" && status != "Denied" {
			clean = append(clean, id)
		}
	}
	return calls, failed, clean
}, func(line []byte) []string {
	m := decodeJSONLine(line)
	ev, ok := m["event"].(map[string]any)
	if !ok {
		return nil
	}
	ts, ok := ev["ToolStart"].(map[string]any)
	if !ok {
		return nil
	}
	name, _ := ts["name"].(string)
	if !claudeDialect.isShellTool(name) {
		return nil
	}
	id, _ := ts["id"].(string)
	if id == "" {
		return nil
	}
	return []string{id}
})

func anteResumes(path string, offset int64) bool {
	if filepath.Base(path) == "dialog.json" {
		return false
	}
	return anteEventsResumes(path, offset)
}
