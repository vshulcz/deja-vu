package sources

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

var flatRolePathTools = func() map[string]bool {
	m := maps.Clone(pathTools)
	for _, k := range []string{
		"read_file", "edit_file", "write_file", "read_multiple_files",
		"read", "write", "edit", "notebook_edit", "str_replace_editor", "multi_edit",
	} {
		m[k] = true
	}
	return m
}()

var flatRoleShellTools = func() map[string]bool {
	m := maps.Clone(claudeShellTools)
	for _, k := range []string{
		"bash", "sh", "shell", "shell_command", "execute_command",
		"powershell", "pwsh", "exec_command", "run_command", "monitor_command",
	} {
		m[k] = true
	}
	return m
}()

var flatRoleDialect = toolDialect{
	pathKey:       "file_path",
	pathKeyAlt:    "path",
	pathTools:     flatRolePathTools,
	pathListKey:   "paths",
	pathListGlobs: true,
	shellTool:     "Bash",
	shellTools:    flatRoleShellTools,
	commandKeyAlt: "cmd",
	argsKey:       "args",
	newKeyAlt:     "new_source",
}

// parseFlatRoleJSONL reads a transcript whose every line is one message —
// `{"role":…,"content":…,"timestamp":…,"sessionId":…}` — with no envelope
// around it. Command Code and ZCode both write that shape under a
// Claude-Code-style `projects/<encoded-cwd>/<session>.jsonl` layout, which is
// what the two have in common and all they have in common (#3647).
func parseFlatRoleJSONL(path string, offset int64, harness, project string) ([]model.Session, error) {
	s := model.Session{
		Harness: harness,
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: project,
		Path:    path,
	}
	exits := commandExits{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) { flatRoleLineWithExits(&s, m, exits) })
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func flatRoleLineWithExits(s *model.Session, m map[string]any, exits commandExits) {
	role, _ := m["role"].(string)
	if role == "" {
		return
	}
	blocks, results, speech, onlyTool := unpackContentBlocks(m["content"])
	isToolRole := role != "user" && role != "assistant"
	if isToolRole && len(results) == 0 && speech != "" {
		results = append(results, speech)
		blocks = append(blocks, flatRoleToolResultBlock(m, speech))
		speech = ""
		onlyTool = true
	}
	if speech == "" && len(blocks) == 0 && len(results) == 0 {
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
	if id, _ := m["sessionId"].(string); id != "" {
		s.ID = id
	}
	t := parseTimeAny(m["timestamp"])
	s.Touch(t)
	if speech != "" {
		msgRole := role
		if isToolRole || onlyTool {
			msgRole = RoleToolOutput
		}
		if msgRole != RoleToolOutput || IndexToolOutput() {
			s.Messages = append(s.Messages, model.Message{Role: msgRole, Text: capParsedMessage(speech), Time: t})
		}
	}
	from := len(s.Messages)
	s.Messages = append(s.Messages, dialectWorkRecords(blocks, results, flatRoleDialect, t)...)
	if exits != nil {
		joinResultExits(s.Messages, from, blocks, flatRoleDialect, exits, flatRoleResultExitCode)
	}
}

func flatRoleToolResultBlock(m map[string]any, body string) map[string]any {
	callID := firstNonEmptyStr(str(m["tool_call_id"]), str(m["tool_use_id"]), str(m["id"]), inlineToolID)
	tb := map[string]any{
		"type":        "tool_result",
		"tool_use_id": callID,
		"content":     body,
	}
	if code, ok := anyInt(m["exit_code"]); ok {
		tb["exit_code"] = code
	} else if code, ok := anyInt(m["exitCode"]); ok {
		tb["exit_code"] = code
	}
	if bad, _ := m["is_error"].(bool); bad {
		tb["is_error"] = true
	}
	return tb
}

func flatRoleResultExitCode(result map[string]any) (int, bool) {
	if code, ok := anyInt(result["exit_code"]); ok {
		return code, true
	}
	if code, ok := anyInt(result["exitCode"]); ok {
		return code, true
	}
	body := contentText(result["content"])
	if body == "" {
		body, _ = result["text"].(string)
	}
	if code, ok := parseExitStatusLine(body); ok {
		return code, true
	}
	if bad, _ := result["is_error"].(bool); bad {
		return 1, true
	}
	if id, _ := result["tool_use_id"].(string); id != "" {
		return 0, true
	}
	return 0, false
}

func parseExitStatusLine(body string) (int, bool) {
	if body == "" {
		return 0, false
	}
	first := firstLine(body)
	last := lastLine(body)
	for _, prefix := range []string{"Exit code ", "Command exited with code ", "Command failed with exit code "} {
		suffix := ""
		if strings.HasSuffix(prefix, "with exit code ") {
			suffix = "."
		}
		if n, ok := statusCode(first, prefix, suffix); ok {
			return n, true
		}
		if last != first {
			if n, ok := statusCode(last, prefix, suffix); ok {
				return n, true
			}
		}
	}
	colonLine := first
	if at := strings.Index(colonLine, " ("); at > 0 && strings.HasSuffix(colonLine, ")") {
		colonLine = colonLine[:at]
	}
	if n, ok := statusCode(colonLine, "Exit code: ", ""); ok {
		return n, true
	}
	return 0, false
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func anyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

var flatRoleExitResumes = resumesUnlessSettling("tool", func(m map[string]any) (calls, failed, clean []string) {
	blocks, _, _, _ := unpackContentBlocks(m["content"])
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
	if role, _ := m["role"].(string); role != "" && role != "user" && role != "assistant" {
		if body := contentText(m["content"]); body != "" {
			tb := flatRoleToolResultBlock(m, body)
			id := str(tb["tool_use_id"])
			code, hasCode := flatRoleResultExitCode(tb)
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
	blocks, _, _, _ := unpackContentBlocks(m["content"])
	var ids []string
	for _, c := range commandCallsIn(blocks, flatRoleDialect) {
		ids = append(ids, c.ID)
	}
	return ids
})
