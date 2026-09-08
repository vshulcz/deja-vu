package sources

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CopilotRoot returns the GitHub Copilot CLI session-state root, overridable
// via DEJA_COPILOT_ROOT. Each session lives in its own UUID directory as an
// append-only events.jsonl.
func CopilotRoot() string {
	return EnvPath("DEJA_COPILOT_ROOT", filepath.Join(Home(), ".copilot", "session-state"))
}

// CopilotSessionFiles lists event logs under the Copilot session root.
func CopilotSessionFiles() []string {
	return walkFiles(CopilotRoot(), func(p string) bool {
		return filepath.Base(p) == "events.jsonl"
	})
}

// CopilotSidecarFiles lists the IDE's bookkeeping beside each session —
// vscode.metadata.json — which doctor counted as a transcript deja could not
// read, once per session (#3303).
func CopilotSidecarFiles() []string {
	return walkFiles(CopilotRoot(), func(p string) bool {
		return filepath.Base(p) == "vscode.metadata.json"
	})
}

// LoadCopilot loads all Copilot CLI sessions.
func LoadCopilot() []model.Session { return parseFiles(CopilotSessionFiles(), ParseCopilotFile) }

// copilotSkillContextRe is the wrapper Copilot CLI puts around a skill's body
// when it injects it as a user.message: the block is the host's, and the
// whole skill indexed as the person's words (#3305). A person's words beside
// it stay.
var copilotSkillContextRe = regexp.MustCompile(`(?s)<skill-context\b[^>]*>.*?</skill-context>`)

func copilotStripSkillContext(txt string) string {
	if !strings.Contains(txt, "<skill-context") {
		return txt
	}
	return strings.TrimSpace(copilotSkillContextRe.ReplaceAllString(txt, ""))
}

// ParseCopilotFile parses a single Copilot events.jsonl.
func ParseCopilotFile(path string) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, 0)
}

// ParseCopilotFileFromOffset parses a Copilot event log starting at a byte offset.
func ParseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, offset)
}

// copilotDialect is Copilot's tool vocabulary, read off events.jsonl its own
// CLI wrote. Names are lowercase where Claude's are capitalised, the file key
// is `path`, and the replaced span is `old_str` rather than `old_string` —
// reading the wrong one loses the only record of what stopped existing.
var copilotDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"edit": true, "read": true, "write": true, "create": true},
	shellTool: "bash",
	editTools: map[string]bool{"edit": true},
	oldKey:    "old_str",
}

func parseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "copilot",
		ID:      filepath.Base(filepath.Dir(path)),
		Path:    path,
	}
	// Which message holds each shell command, by the call id the completion
	// event repeats: Copilot files the command and its outcome as two records.
	commandAt := map[string][]int{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		data, _ := m["data"].(map[string]any)
		t := parseTimeAny(m["timestamp"])
		switch typ {
		case "session.start":
			if data == nil {
				return
			}
			if id, _ := data["sessionId"].(string); id != "" {
				s.ID = id
			}
			s.Touch(parseTimeAny(data["startTime"]))
			if ctx, ok := data["context"].(map[string]any); ok {
				if cwd, _ := ctx["cwd"].(string); cwd != "" {
					s.Project = copilotProjectName(cwd)
				}
			}
		case "user.message", "assistant.message":
			if data == nil {
				return
			}
			role := "user"
			if typ == "assistant.message" {
				role = "assistant"
			}
			s.Touch(t)
			txt, _ := data["content"].(string)
			if role == "user" {
				txt = copilotStripSkillContext(txt)
			}
			if txt != "" {
				s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
			}
		case "tool.execution_start":
			// The call carries the work: which file, what it replaced, what
			// command ran. Copilot files these outside the message stream, so
			// the assistant turns that do nothing but call tools were being
			// stored as empty and the work was reachable from nothing.
			if data == nil {
				return
			}
			name, _ := data["toolName"].(string)
			args, _ := data["arguments"].(map[string]any)
			if name == "" || args == nil {
				return
			}
			part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
			var records []model.Message
			if IndexToolPaths() {
				if p := toolPathsIn(part, copilotDialect); p != "" {
					records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexEdits() {
				for _, span := range editSpansIn(part, copilotDialect) {
					records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
				}
			}
			// Every command of the call, not the last one: a dialect that
			// hands back an array would otherwise mark one of them.
			var cmdAt []int
			if IndexCommands() {
				for _, cmd := range commandsIn(part, copilotDialect) {
					cmdAt = append(cmdAt, len(records))
					records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
				}
			}
			if len(records) == 0 {
				return
			}
			s.Touch(t)
			if id, _ := data["toolCallId"].(string); id != "" && len(cmdAt) > 0 {
				// Assigned, not appended: an id reused for a later call would
				// otherwise carry the first call's commands too, and the second
				// outcome would land on a run that had already finished
				// (review of #3369).
				at := make([]int, 0, len(cmdAt))
				for _, i := range cmdAt {
					at = append(at, len(s.Messages)+i)
				}
				commandAt[id] = at
			}
			s.Messages = append(s.Messages, records...)
		case "tool.execution_complete":
			// Kept whether or not the call succeeded: the error a command hit
			// is exactly what a later search reaches for, and copilot's own
			// failure text was unreachable before this.
			if !IndexToolOutput() || data == nil {
				return
			}
			result, _ := data["result"].(map[string]any)
			out, _ := result["content"].(string)
			// What the command did, which Copilot does not put in `success`:
			// that field is true on every completion in a real store, failed
			// runs included, and the code lives in the telemetry and in a
			// trailer inside the output (#3369).
			if code := copilotExitCode(data, out); code > 0 {
				if id, _ := data["toolCallId"].(string); id != "" {
					for _, i := range commandAt[id] {
						if i < len(s.Messages) {
							s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
						}
					}
					// Consumed: one outcome belongs to one call.
					delete(commandAt, id)
				}
			}
			if out = strings.TrimSpace(out); out == "" {
				return
			}
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// jsonInt reads a number the scanner decoded with UseNumber, and the two other
// shapes a store can hold it in.
func jsonInt(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		// A store that wrote the code as 1.0 says the same thing.
		if f, err := n.Float64(); err == nil && f == float64(int(f)) {
			return int(f), true
		}
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true
		}
	}
	return 0, false
}

// copilotShellExitRe is the trailer Copilot writes under a shell run's output:
// "<shellId: 0 completed with exit code 1>". The tool's own documentation
// mentions the words "exit code" in prose, which is why this asks for the
// whole shape rather than the phrase.
var copilotShellExitRe = regexp.MustCompile(`<shellId:[^>]*completed with exit code (\d+)>`)

// copilotExitCode reads what a shell call actually did. The telemetry carries
// the number when Copilot recorded one; the trailer under the output is the
// fallback, and both are absent for a call that is not a shell run.
func copilotExitCode(data map[string]any, out string) int {
	if tel, ok := data["toolTelemetry"].(map[string]any); ok {
		if metrics, ok := tel["metrics"].(map[string]any); ok {
			// The scanner decodes with UseNumber, so a JSON number arrives as
			// json.Number and never as float64: asserting the latter made this
			// whole path dead code and left every marker to the trailer below
			// (review of #3369).
			if code, ok := jsonInt(metrics["exit_code"]); ok {
				return code
			}
		}
	}
	if m := copilotShellExitRe.FindStringSubmatch(out); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil {
			return code
		}
	}
	return 0
}

// copilotProjectName mirrors the codex convention: the last two path segments
// of the recorded working directory, or the final one at filesystem roots.
func copilotProjectName(cwd string) string {
	cwd = strings.TrimRight(cwd, "/\\")
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	parent := filepath.Base(filepath.Dir(cwd))
	if parent != "" && parent != "." && parent != string(filepath.Separator) && !strings.Contains(parent, ":") {
		return parent + "/" + base
	}
	return base
}
