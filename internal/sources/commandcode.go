package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Command Code (commandcode.ai) writes one transcript per session under a
// Claude-Code-style project directory:
//
//	~/.commandcode/projects/<encoded-cwd>/<session>.jsonl
//
// Since 1.73 the file is a `session` header line carrying the id and the cwd,
// then one `{"type":"message","message":{"role","content"}}` envelope per line,
// the content in Claude's blocks under Command Code's own tool names. The older
// shape, one flat role/content line each, is migrated to it in place the next
// time the client opens the session, keeping `<session>.v2.bak` (#4370).
//
// Beside it sits `<session>.checkpoints.jsonl`, a snapshot stream rather than a
// conversation — read as a transcript it adds a session with no words in it, so
// it is skipped by name (#3647), and so are the other names the client's own
// transcript filter skips.

// CommandCodeRoot is the project store root. DEJA_COMMANDCODE_ROOT replaces it.
func CommandCodeRoot() string {
	return EnvPath("DEJA_COMMANDCODE_ROOT", filepath.Join(Home(), ".commandcode", "projects"))
}

// CommandCodeSessionFiles lists the transcripts, checkpoints excluded.
func CommandCodeSessionFiles() []string {
	return walkFiles(CommandCodeRoot(), commandCodeIsTranscript)
}

// commandCodeIsTranscript reports whether a path is a conversation rather than
// one of the streams that share its extension. The test is the client's own
// (isSessionTranscriptFileName in command-code 1.73.4).
func commandCodeIsTranscript(p string) bool {
	base := filepath.Base(p)
	return strings.HasSuffix(base, ".jsonl") && !strings.Contains(base, ".checkpoints.") &&
		!strings.Contains(base, ".prompts.") && !strings.Contains(base, ".v2.bak")
}

// CommandCodeCheckpointFiles lists the snapshot streams, prompt histories and
// `<session>.meta.json` sidecars sitting beside the transcripts. deja does not read them — they are not
// conversations — and they are named so `deja doctor` can count them as a deliberate skip rather than
// as a file it failed to understand, which is how a store reports drift.
func CommandCodeCheckpointFiles() []string {
	return walkFiles(CommandCodeRoot(), func(p string) bool {
		if strings.HasSuffix(p, ".meta.json") {
			return true
		}
		return strings.HasSuffix(p, ".jsonl") && !commandCodeIsTranscript(p)
	})
}

// CommandCodeUnderRoot lets the registry claim a path for incremental ingest.
func CommandCodeUnderRoot(p string) bool {
	return strings.HasPrefix(p, CommandCodeRoot()) && commandCodeIsTranscript(p)
}

func LoadCommandCode() []model.Session {
	return parseFiles(CommandCodeSessionFiles(), ParseCommandCodeFile)
}

// ParseCommandCodeFile reads one transcript.
func ParseCommandCodeFile(path string) ([]model.Session, error) {
	return ParseCommandCodeFileFromOffset(path, 0)
}

// ParseCommandCodeFileFromOffset is the incremental read.
func ParseCommandCodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseCommandCodeWith(path, func(fn func(map[string]any)) error {
		return scanJSONLWithHeaderFromOffset(path, offset, fn)
	})
}

// parseCommandCodeWith reads the records scan hands over as one transcript at
// path; the compaction capture hands over the records before a summary.
func parseCommandCodeWith(path string, scan func(func(map[string]any)) error) ([]model.Session, error) {
	s := model.Session{
		Harness: "commandcode",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: commandCodeProject(path),
		Path:    path,
	}
	exits := commandExits{}
	err := scan(func(m map[string]any) {
		switch typ, _ := m["type"].(string); typ {
		case "session":
			// The folder name is a lossy slug of the cwd; the header has it
			// whole.
			applyPiHeader(&s, m, true)
		case "message":
			commandCodeMessage(&s, m, exits)
		case "compaction":
			// The summary a compaction wrote; the turns it replaced stay in
			// the file above it.
			if text, _ := m["summary"].(string); strings.TrimSpace(text) != "" {
				t := parseTimeAny(m["timestamp"])
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: text, Time: t})
			}
		default:
			flatRoleLine(&s, m)
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// commandCodeDialect is the tool vocabulary in command-code 1.73.4's bundle:
// Claude's input keys under snake_case names. 1.74.0 runs commands through
// shell_command and monitor_command as {command, args[]} and powershell
// {command}, and read_file's paths list takes globs beside files (#4540).
var commandCodeDialect = toolDialect{
	pathKey:       "file_path",
	pathTools:     map[string]bool{"read_file": true, "edit_file": true, "write_file": true, "read_multiple_files": true},
	pathListKey:   "paths",
	pathListGlobs: true,
	shellTool:     "shell_command",
	shellTools:    map[string]bool{"shell_command": true, "powershell": true, "monitor_command": true},
	argsKey:       "args",
	editTools:     map[string]bool{"edit_file": true, "write_file": true},
}

// commandCodeMessage reads one v3 envelope. A user-role message made of
// tool_result blocks is tool output, as in a Claude transcript; such a result
// says how the command of the call it answers ended.
func commandCodeMessage(s *model.Session, m map[string]any, exits commandExits) {
	msg, ok := m["message"].(map[string]any)
	if !ok {
		return
	}
	role, _ := msg["role"].(string)
	if role != "user" && role != "assistant" {
		return
	}
	t := parseTimeAny(m["timestamp"])
	s.Touch(t)
	content := msg["content"]
	txt, toolOut := textFromContentKind(content)
	if toolOut {
		role = RoleToolOutput
	}
	if txt != "" {
		s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
	}
	if IndexToolPaths() {
		if p := toolPathsIn(content, commandCodeDialect); p != "" {
			s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexEdits() {
		for _, e := range editSpansIn(content, commandCodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: e, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(content, commandCodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexCommands() {
		from := len(s.Messages)
		for _, c := range commandsIn(content, commandCodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: c, Time: t})
		}
		joinResultExits(s.Messages, from, content, commandCodeDialect, exits, commandCodeExitCode)
	}
}

// commandCodeExitCode reads the status Command Code opens a failed command's
// result with: "Exit code: N", or "Exit code: N (<meaning>)" for a code it
// knows, such as grep's 1 (formatShellCommandResult). A clean run has no such
// line, and no result carries is_error (#4539).
func commandCodeExitCode(result map[string]any) (int, bool) {
	line := firstLine(contentText(result["content"]))
	if at := strings.Index(line, " ("); at > 0 && strings.HasSuffix(line, ")") {
		line = line[:at]
	}
	code, ok := statusCode(line, "Exit code: ", "")
	return code, ok && code != 0
}

// commandCodeExitResumes is the #4443 rule for Command Code's v3 envelopes: a
// tail holding the failed result of a call stored already is read whole, or
// the call never gets its exit; a clean one is let go.
var commandCodeExitResumes = resumesUnlessAnswering(`"tool_`, func(m map[string]any) ([]string, string) {
	msg, _ := m["message"].(map[string]any)
	items, _ := msg["content"].([]any)
	var calls []string
	for _, it := range items {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "tool_use":
			calls = append(calls, str(p["id"]))
		case "tool_result":
			if _, failed := commandCodeExitCode(p); failed && str(p["tool_use_id"]) != "" {
				return calls, str(p["tool_use_id"])
			}
		}
	}
	return calls, ""
})

// commandCodeProject names the project from the header's cwd when it has one;
// the folder name is a lossy slug of it (my-app and my/app share one).
func commandCodeProject(path string) string {
	if cwd := CommandCodeSessionDir(path); cwd != "" {
		return cwdProjectName(cwd)
	}
	dir := projectDir(CommandCodeRoot(), path)
	if dir == "" || dir == CommandCodeRoot() {
		return ""
	}
	return claudeProjectName(dir)
}

// CommandCodeSessionDir is the directory a session ran in: the cwd on the v3
// header line, or on any of the first records that carries one. The folder
// name is a lossy slug of it. "" when none names an absolute path (#4372).
func CommandCodeSessionDir(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for i := 0; i < claudeCWDScanLines; i++ {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"cwd"`)) {
			var v struct {
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(line, &v) == nil && filepath.IsAbs(v.CWD) {
				return v.CWD
			}
		}
		if err != nil {
			return ""
		}
	}
	return ""
}
