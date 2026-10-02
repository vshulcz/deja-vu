package sources

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// QwenConfigDir is the native Qwen Code configuration directory. DEJA_QWEN_ROOT
// intentionally does not affect it because that variable only relocates reads.
func QwenConfigDir() string { return filepath.Join(Home(), ".qwen") }

func QwenRoot() string { return EnvPath("DEJA_QWEN_ROOT", QwenConfigDir()) }

func QwenSessionFiles() []string {
	return walkFiles(filepath.Join(QwenRoot(), "projects"), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl") && filepath.Base(filepath.Dir(p)) == "chats"
	})
}

// QwenSidecarFiles are the files qwen keeps beside its transcripts that are not
// conversations: `<id>.runtime.json` per session, and `meta.json` and
// `extract-cursor.json` per project. Counted as unread transcripts they made
// doctor say "11 not recognised here" about a store it reads correctly — the
// same wrong claim Continue's `sessions.json` and Kimi's `state.json` used to
// produce (#3676).
func QwenSidecarFiles() []string {
	return walkFiles(filepath.Join(QwenRoot(), "projects"), func(p string) bool {
		base := filepath.Base(p)
		switch {
		case strings.HasSuffix(base, ".runtime.json"):
			return true
		case base == "meta.json" || base == "extract-cursor.json":
			return true
		// Qwen 0.20's session groups and workflow runs, and the metadata
		// beside a sub-agent's log (#4475).
		case base == "session-organization.v1.json":
			return true
		case qwenUnder(p, "workflows"):
			return true
		case qwenUnder(p, "subagents") && strings.HasSuffix(base, ".meta.json"):
			return true
		}
		return false
	})
}

// QwenSubagentFile reports whether p is a sub-agent's log,
// projects/<project>/subagents/<session>/agent-<id>.jsonl. The reader takes
// only chats/, so doctor counts these as skipped, not unread (#4475).
func QwenSubagentFile(p string) bool {
	return strings.HasSuffix(p, ".jsonl") && qwenUnder(p, "subagents")
}

// qwenUnder reports whether p sits in the named directory of its project.
func qwenUnder(p, dir string) bool {
	rel, err := filepath.Rel(filepath.Join(QwenRoot(), "projects"), p)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) > 2 && parts[1] == dir
}

func LoadQwen() []model.Session { return parseFiles(QwenSessionFiles(), ParseQwenFile) }

// QwenProjectDirBase returns the encoded project dir name for a transcript
// path, e.g. "-Users-x-projects-app" for
// .../projects/-Users-x-projects-app/chats/s.jsonl. qwen resumes a session
// only from the directory it belongs to.
func QwenProjectDirBase(path string) string {
	dir := projectDir(filepath.Join(QwenRoot(), "projects"), path)
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// Qwen Code names a project folder the way Claude Code does — sanitizeCwd,
// every character outside [A-Za-z0-9] as "-", the path lowercased first on
// Windows — so a directory named in Cyrillic, CJK or with accents cannot be
// read back from the folder name (#4258). Every record carries the real
// directory as cwd; it is trusted when it encodes to the folder it was found
// in.
func qwenFolderIs(cwd, base string) bool {
	enc := claudeEncodePath(cwd)
	return enc == base || strings.ToLower(enc) == base
}

// qwenTranscriptCWD is the directory the transcript at path records, when its
// folder was named for it; "" otherwise.
func qwenTranscriptCWD(path string) string {
	base := QwenProjectDirBase(path)
	if base == "" {
		return ""
	}
	return transcriptCWD(path, func(cwd string) bool { return qwenFolderIs(cwd, base) })
}

// QwenSessionDir is the directory a Qwen Code session ran in, for the cd in
// front of `qwen -r`, and whether the transcript recorded it. A recorded cwd
// is returned whether or not it still exists: the folder name is ambiguous —
// /w/my-app and /w/my/app encode the same — so it is resolved on disk only
// for a transcript that records none (#4259).
func QwenSessionDir(path string) (dir string, recorded bool) {
	if cwd := qwenTranscriptCWD(path); cwd != "" {
		return cwd, true
	}
	base := QwenProjectDirBase(path)
	if base == "" {
		return "", false
	}
	return ResolveEncodedPath(base), false
}

func ParseQwenFile(path string) ([]model.Session, error) {
	return parseQwenFileFromOffset(path, 0)
}

func ParseQwenFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseQwenFileFromOffset(path, offset)
}

func parseQwenFileFromOffset(path string, offset int64) ([]model.Session, error) {
	project := cwdProjectName(qwenTranscriptCWD(path))
	if project == "" {
		project = claudeProjectName(projectDir(filepath.Join(QwenRoot(), "projects"), path))
	}
	s := model.Session{
		Harness: "qwen",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Project: project,
		Path:    path,
	}
	// A shell call and its result are separate records; the call id carries
	// the command over to the result its exit status is read from (#4255).
	shellAt := map[string]int{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		typ, _ := m["type"].(string)
		if typ == "tool_result" {
			// Every tool result is its own record, role user, parts holding
			// the functionResponse. Skipped with the other types, a failing
			// command's error never reached search or the fix pairs (#3281).
			t := parseTimeAny(m["timestamp"])
			// Touched like a user or assistant record, output or not: the
			// session's clock moves with every record it holds.
			s.Touch(t)
			if msg, ok := m["message"].(map[string]any); ok {
				s.Messages = append(s.Messages, qwenWorkRecords(msg["parts"], t)...)
				qwenNoteExits(&s, msg["parts"], shellAt)
			}
			return
		}
		if typ != "user" && typ != "assistant" {
			return
		}
		if id, _ := m["sessionId"].(string); id != "" {
			s.ID = id
		}
		t := parseTimeAny(m["timestamp"])
		s.Touch(t)
		role := typ
		text := ""
		if msg, ok := m["message"].(map[string]any); ok {
			if r, _ := msg["role"].(string); r != "" {
				switch r {
				case "model":
					role = "assistant"
				case "user":
					role = "user"
				default:
					role = typ
				}
			}
			text = qwenText(msg["parts"])
		}
		if text != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		}
		// The work sits in the same parts list, as functionCall and
		// functionResponse rather than text, so qwenText walked past it and
		// the commands a session ran were reachable from nothing.
		if msg, ok := m["message"].(map[string]any); ok {
			start := len(s.Messages)
			s.Messages = append(s.Messages, qwenWorkRecords(msg["parts"], t)...)
			qwenNoteShellCalls(&s, msg["parts"], start, shellAt)
			qwenNoteExits(&s, msg["parts"], shellAt)
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// qwenDialect is Qwen Code's tool vocabulary. The names follow Gemini's — Qwen
// Code is built in that shape — with `file_path` for the file tools and
// `command` for the shell. Qwen 0.20 renamed `replace` to `edit` and still
// maps the old name to it, so both are read; write_file is an edit too, the
// whole file its written side (#4254). Gemini CLI reads through this dialect
// and still says `replace`.
var qwenDialect = toolDialect{
	pathKey:   "file_path",
	pathTools: map[string]bool{"read_file": true, "write_file": true, "replace": true, "edit": true, "read_many_files": true},
	shellTool: "run_shell_command",
	editTools: map[string]bool{"replace": true, "edit": true, "write_file": true},
	oldKey:    "old_string",
	// read_many_files names no file under file_path: Gemini CLI 0.60 takes
	// its paths and globs as a list under `include` (#4494).
	pathListKey:   "include",
	pathListGlobs: true,
}

// qwenNoteShellCalls maps the id of each shell call among parts to the
// command record it produced in s.Messages[start:].
func qwenNoteShellCalls(s *model.Session, parts any, start int, shellAt map[string]int) {
	items, _ := parts.([]any)
	used := map[int]bool{}
	for _, part := range items {
		p, _ := part.(map[string]any)
		call, ok := p["functionCall"].(map[string]any)
		if !ok {
			continue
		}
		id, _ := call["id"].(string)
		if id == "" {
			continue
		}
		// An id seen again belongs to this call now, recorded or not.
		delete(shellAt, id)
		name, _ := call["name"].(string)
		args, _ := call["args"].(map[string]any)
		cmd, _ := args["command"].(string)
		if !qwenDialect.isShellTool(name) || cmd == "" {
			continue
		}
		for i := start; i < len(s.Messages); i++ {
			if !used[i] && s.Messages[i].Role == RoleCommand && s.Messages[i].Text == "$ "+cmd {
				shellAt[id], used[i] = i, true
				break
			}
		}
	}
}

// qwenNoteExits appends a non-zero exit to the command a functionResponse
// among parts answers. Qwen writes Gemini's footer: "Exit Code: 128" after
// the output, under `error` when the call failed.
func qwenNoteExits(s *model.Session, parts any, shellAt map[string]int) {
	items, _ := parts.([]any)
	for _, part := range items {
		p, _ := part.(map[string]any)
		resp, ok := p["functionResponse"].(map[string]any)
		if !ok {
			continue
		}
		id, _ := resp["id"].(string)
		i, ok := shellAt[id]
		if !ok {
			continue
		}
		r, _ := resp["response"].(map[string]any)
		out, _ := r["output"].(string)
		code := geminiExitCode(out)
		if code == 0 {
			errOut, _ := r["error"].(string)
			code = geminiExitCode(errOut)
		}
		if code > 0 {
			s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
		}
		delete(shellAt, id)
	}
}

// qwenWorkRecords turns the functionCall and functionResponse parts of one
// message into work records. The parts are rewritten into the tool_use shape
// the shared extractors read, so Qwen does not need its own copy of the
// extraction.
func qwenWorkRecords(v any, t time.Time) []model.Message {
	parts, ok := v.([]any)
	if !ok {
		return nil
	}
	var calls []any
	var results []string
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if call, ok := m["functionCall"].(map[string]any); ok {
			name, _ := call["name"].(string)
			args, _ := call["args"].(map[string]any)
			if name != "" && args != nil {
				calls = append(calls, map[string]any{
					"type": "tool_use", "name": name, "input": args,
				})
			}
		}
		if resp, ok := m["functionResponse"].(map[string]any); ok {
			r, _ := resp["response"].(map[string]any)
			out, _ := r["output"].(string)
			if out = strings.TrimSpace(out); out == "" {
				// A failed call answers in `error`, the field the fix pairs
				// are mined from (#3281).
				out, _ = r["error"].(string)
				out = strings.TrimSpace(out)
			}
			// The shell's result is a report, not the output: indexed as is,
			// qwen's `Error: (none)` made every command read as failed and the
			// error was stored as `Output: <error>`, which no lookup asks for
			// (#4256).
			if name, _ := resp["name"].(string); qwenDialect.isShellTool(name) {
				out = strings.TrimSpace(UnwrapShellReport(out))
				// A command that printed nothing and hit no error: `(empty)`
				// is the report's word for it, not output.
				if out == "(empty)" {
					out = ""
				}
			}
			if out != "" {
				results = append(results, capParsedMessage(out))
			}
		}
	}
	var recs []model.Message
	if len(calls) > 0 {
		if IndexToolPaths() {
			if p := toolPathsIn(calls, qwenDialect); p != "" {
				recs = append(recs, model.Message{Role: RoleFiles, Text: p, Time: t})
			}
		}
		if IndexWrites() {
			for _, w := range wroteRecordsIn(calls, qwenDialect) {
				recs = append(recs, model.Message{Role: RoleWrote, Text: w, Time: t})
			}
		}
		if IndexEdits() {
			for _, span := range editSpansIn(calls, qwenDialect) {
				recs = append(recs, model.Message{Role: RoleEdit, Text: span, Time: t})
			}
		}
		if IndexCommands() {
			for _, cmd := range commandsIn(calls, qwenDialect) {
				recs = append(recs, model.Message{Role: RoleCommand, Text: cmd, Time: t})
			}
		}
	}
	if IndexToolOutput() {
		for _, out := range results {
			recs = append(recs, model.Message{Role: RoleToolOutput, Text: out, Time: t})
		}
	}
	return recs
}

func qwenText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if thought, _ := m["thought"].(bool); thought {
			continue
		}
		text, _ := m["text"].(string)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String()
}

// UnwrapShellReport strips the frame gemini and qwen put around a command's
// output before handing it to the model. Gemini fences it in
// <untrusted_context> with an "Output:" marker; qwen writes a labelled report —
// Command, Directory, Output, Error, Exit Code, Signal, PGID. The marker is
// what matters: with it in front, the first line of a build failure stops
// looking like an error, and the fix pair went silent on a failure it answers
// the moment the marker is gone (gemini-cli 0.55.1, qwen-code 0.20.0). The
// index and the failure hook both read it through here, so the error a pair is
// stored under is the one the hook looks up (#4256).
func UnwrapShellReport(s string) string {
	if !strings.Contains(s, "Output:") {
		return s
	}
	var kept []string
	labelled := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		// `Error: (none)` is qwen's report saying there was no error. A real
		// one keeps its label: it is the failure, and a line the command
		// printed itself may start the same way.
		if t == "<untrusted_context>" || t == "</untrusted_context>" || t == "Error: (none)" || shellReportLabel(t) {
			continue
		}
		// The label introduces the payload on its first line only; what follows
		// is the command's own output, untouched.
		if !labelled && strings.HasPrefix(line, "Output: ") {
			line, labelled = line[len("Output: "):], true
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// shellReportLabel reports whether a line is part of the shell report rather
// than the command's output.
func shellReportLabel(t string) bool {
	for _, label := range []string{"Command: ", "Directory: ", "Exit Code: ", "Signal: ", "Background PIDs: ", "Process Group PGID:"} {
		if strings.HasPrefix(t, label) {
			return true
		}
	}
	return false
}
