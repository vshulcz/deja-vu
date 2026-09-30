package sources

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// PiConfigDir is the native pi coding agent configuration directory.
func PiConfigDir() string { return filepath.Join(Home(), ".pi", "agent") }

// PiRoot returns the session store root, overridable via DEJA_PI_ROOT.
func PiRoot() string { return EnvPath("DEJA_PI_ROOT", filepath.Join(PiConfigDir(), "sessions")) }

// PiSessionFiles lists transcript files under the pi session root.
func PiSessionFiles() []string {
	return walkFiles(PiRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// LoadPi loads all pi sessions.
func LoadPi() []model.Session { return parseFiles(PiSessionFiles(), ParsePiFile) }

// ParsePiFile parses a single pi session transcript.
func ParsePiFile(path string) ([]model.Session, error) {
	return parsePiFileFromOffset(path, 0)
}

// ParsePiFileFromOffset parses a pi session transcript starting at a byte offset.
func ParsePiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiFileFromOffset(path, offset)
}

func parsePiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parsePiShaped(path, offset, "pi", piProjectName(path), false)
}

// parsePiShaped parses a pi-format transcript (shared by pi and OpenClaw,
// whose agent runtime is the same lineage). useHeaderCwd promotes the session
// header's cwd to the project key when present.
func parsePiShaped(path string, offset int64, harness, project string, useHeaderCwd bool) ([]model.Session, error) {
	s := model.Session{
		Harness: harness,
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: project,
		Path:    path,
	}
	cwd := ""
	err := scanJSONLWithHeaderFromOffset(path, offset, func(m map[string]any) { piShapedLine(&s, m, useHeaderCwd, &cwd) })
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// piShapedLine folds one transcript line into s: the session header and the
// user/assistant/toolResult messages. Shared by the JSONL transcripts and
// OpenClaw's SQLite store, whose event_json rows are the same lines.
func piShapedLine(s *model.Session, m map[string]any, useHeaderCwd bool, cwd *string) {
	typ, _ := m["type"].(string)
	switch typ {
	case "session":
		applyPiHeader(s, m, useHeaderCwd)
		*cwd, _ = m["cwd"].(string)
	case "message":
		msg, ok := m["message"].(map[string]any)
		if !ok {
			return
		}
		role, _ := msg["role"].(string)
		outRole := role
		switch role {
		case "user", "assistant":
			// speech, kept under its own role
		case "toolResult":
			// Command output and errors carry the same recall value every
			// other harness with structured tool output indexes: roleToolOutput
			// powers friction and `--role tool`. pi kept it in the transcript
			// but the parser dropped everything but speech, so pi users alone
			// had no friction and could not search a command's output.
			outRole = RoleToolOutput
		default:
			return
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		txt := textFromContent(msg["content"])
		if txt != "" {
			s.Messages = append(s.Messages, model.Message{Role: outRole, Text: txt, Time: t})
		}
		if role == "assistant" {
			raw := piToolCalls(msg["content"], *cwd, s.Harness)
			if IndexToolPaths() {
				if paths := claudeToolPaths(raw); paths != "" {
					s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: paths, Time: t})
				}
			}
			if IndexEdits() {
				for _, span := range claudeEditSpans(raw) {
					s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: t})
				}
			}
			if IndexWrites() {
				for _, record := range claudeWroteRecords(raw) {
					s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: record, Time: t})
				}
			}
			if IndexCommands() {
				for _, command := range claudeCommands(raw) {
					s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: command.Text, Time: t})
				}
			}
		}
	}
}

// applyPiHeader reads identity out of the `session` header line, whether it
// arrived in the scan or was fetched separately because the scan began past it.
func applyPiHeader(s *model.Session, m map[string]any, useHeaderCwd bool) {
	if typ, _ := m["type"].(string); typ != "session" {
		return
	}
	if id, _ := m["id"].(string); id != "" {
		s.ID = id
	}
	if useHeaderCwd {
		if cwd, _ := m["cwd"].(string); cwd != "" {
			s.Project = claudeProjectName(pathToProjectKey(cwd))
		}
	}
	s.Touch(parseTimeAny(m["timestamp"]))
}

// piProjectName derives the project display name from the encoded directory
// name. pi uses the same "--" encoding as Claude Code.
func piProjectName(path string) string {
	dir := projectDir(PiRoot(), path)
	return claudeProjectName(dir)
}

// PiProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "--Users-x-projects-app--" for .../sessions/--Users-x-projects-app--/s.jsonl.
func PiProjectDirBase(path string) string {
	dir := projectDir(PiRoot(), path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// piToolCalls adapts known pi tool arguments to the shared extractors. Keeping
// their filtering and size limits also keeps restore and blame records aligned
// with the other harnesses. Relative paths resolve against the session header,
// including when an incremental scan fetches that header separately.
func piToolCalls(content any, cwd, harness string) json.RawMessage {
	blocks, _ := content.([]any)
	var calls []map[string]any
	for _, block := range blocks {
		part, ok := block.(map[string]any)
		if !ok || part["type"] != "toolCall" {
			continue
		}
		args, ok := part["arguments"].(map[string]any)
		if !ok {
			continue
		}
		var name string
		input := map[string]any{}
		switch part["name"] {
		case "bash", "exec":
			name = "Bash"
			input["command"] = args["command"]
		case "read":
			name = "Read"
		case "write":
			name = "Write"
			input["content"] = args["content"]
		case "edit":
			name = "MultiEdit"
			input["old_string"] = piEditString(args, "oldText", "old_string")
			input["new_string"] = piEditString(args, "newText", "new_string")
			var edits []map[string]any
			for _, item := range piEdits(args["edits"]) {
				if edit, ok := item.(map[string]any); ok {
					edits = append(edits, map[string]any{
						"old_string": piEditString(edit, "oldText", "old_string"),
						"new_string": piEditString(edit, "newText", "new_string"),
					})
				}
			}
			input["edits"] = edits
		default:
			continue
		}
		if name != "Bash" {
			path, _ := args["path"].(string)
			if harness == "omp" && name == "Read" {
				path = ompReadLineSelector.ReplaceAllString(path, "")
			}
			if path != "" && cwd != "" && !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			input["file_path"] = path
		}
		calls = append(calls, map[string]any{"type": "tool_use", "name": name, "input": input})
	}
	// The input comes from decoded JSON, so all copied values are encodable.
	raw, _ := json.Marshal(calls)
	return raw
}

// omp read records numeric line selectors in the path argument. Other
// harnesses may use colons literally, so only omp reads lose this suffix.
var ompReadLineSelector = regexp.MustCompile(`:(?:[0-9]+(?:-[0-9]*)?|-[0-9]+)$`)

func piEditString(edit map[string]any, primary, alternate string) string {
	if value, ok := edit[primary].(string); ok {
		return value
	}
	value, _ := edit[alternate].(string)
	return value
}

// Pi accepts edits as an array, one object, or JSON encoding either shape.
// The transcript can retain the original arguments before that coercion.
func piEdits(value any) []any {
	if encoded, ok := value.(string); ok {
		if json.Unmarshal([]byte(encoded), &value) != nil {
			return nil
		}
	}
	switch value := value.(type) {
	case []any:
		return value
	case map[string]any:
		return []any{value}
	default:
		return nil
	}
}
