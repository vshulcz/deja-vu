package sources

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodeBuddy Code, Tencent's terminal agent, keeps one JSONL file per session in
// the directory shape Claude Code uses:
//
//	~/.codebuddy/projects/<mangled-cwd>/<session-id>.jsonl
//	~/.codebuddy/projects/<mangled-cwd>/<session-id>/subagents/agent-<id>.jsonl
//
// The records inside are OpenAI Responses-style items, not Claude Code's:
// {type:"message", role, content:[{type:"input_text"|"output_text", text}]},
// {type:"function_call", callId, name, arguments}, {type:"function_call_result",
// callId, output}, plus reasoning, ai-title, custom-title, topic, summary and
// bookkeeping records. Each carries an epoch-millisecond timestamp and the cwd.
// The tool names and arguments are Claude Code's (Bash, Read, Edit, file_path),
// so the Claude dialect reads the calls once they are put in its block shape.
//
// WorkBuddy is the same agent under another product name and writes the same
// store in its own home, so both are read here. Its two editions keep separate
// homes: ~/.workbuddy (workbuddy.cn) and ~/.workbuddy-ai (WorkBuddy AI, from
// workbuddy.ai) — dataFolderName in the app's cli/product.json.

// CodeBuddyConfigDir is $CODEBUDDY_CONFIG_DIR, else ~/.codebuddy — the
// resolver CodeBuddy uses for its own home (resolveCliHomeDir).
func CodeBuddyConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("CODEBUDDY_CONFIG_DIR")); v != "" {
		return v
	}
	return filepath.Join(Home(), ".codebuddy")
}

// WorkBuddyConfigDirs is $WORKBUDDY_CONFIG_DIR, else both editions' homes.
func WorkBuddyConfigDirs() []string {
	if v := strings.TrimSpace(os.Getenv("WORKBUDDY_CONFIG_DIR")); v != "" {
		return []string{v}
	}
	return []string{filepath.Join(Home(), ".workbuddy"), filepath.Join(Home(), ".workbuddy-ai")}
}

// WorkBuddyConfigDir is the home deja wires: $WORKBUDDY_CONFIG_DIR, else the
// first edition's home holding the app's database, else the first on disk,
// else ~/.workbuddy. Existence alone is not enough: WorkBuddy AI creates
// ~/.workbuddy/device-id on its first launch and never reads that home.
func WorkBuddyConfigDir() string {
	dirs := WorkBuddyConfigDirs()
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "workbuddy.db")); err == nil {
			return d
		}
	}
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return dirs[0]
}

// CodeBuddyRoot is the main session store, <config>/projects.
func CodeBuddyRoot() string { return filepath.Join(CodeBuddyConfigDir(), "projects") }

// CodeBuddyRoots is every store to walk: CodeBuddy's, and each WorkBuddy one
// that exists. DEJA_CODEBUDDY_ROOTS, a path list, replaces both.
func CodeBuddyRoots() []string {
	if list := os.Getenv("DEJA_CODEBUDDY_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	out := []string{CodeBuddyRoot()}
	for _, d := range WorkBuddyConfigDirs() {
		wb := filepath.Join(d, "projects")
		if st, err := os.Stat(wb); err == nil && st.IsDir() && filepath.Clean(wb) != filepath.Clean(out[0]) {
			out = append(out, wb)
		}
	}
	return out
}

// codeBuddyRelTo splits p into its parts under root, or nil when it is not
// under it.
func codeBuddyRelTo(root, p string) []string {
	rel, err := filepath.Rel(filepath.Clean(root), p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

// codeBuddyRel is codeBuddyRelTo for whichever store root p sits in.
func codeBuddyRel(p string) []string {
	for _, root := range CodeBuddyRoots() {
		if parts := codeBuddyRelTo(root, p); parts != nil {
			return parts
		}
	}
	return nil
}

// A main transcript is <project>/<id>.jsonl; a sub-agent's is
// <project>/<session>/subagents/agent-<id>.jsonl.
func codeBuddyMainParts(parts []string) bool {
	return len(parts) == 2 && strings.HasSuffix(parts[1], ".jsonl")
}

func codeBuddySubagentParts(parts []string) bool {
	return len(parts) == 4 && parts[2] == "subagents" && strings.HasSuffix(parts[3], ".jsonl")
}

// isCodeBuddySession reports a main transcript under a store root.
func isCodeBuddySession(p string) bool { return codeBuddyMainParts(codeBuddyRel(p)) }

// CodeBuddySessionDir is the directory a CodeBuddy session ran in, as its
// records state it: `codebuddy -r` finds a session only from there (#4707).
func CodeBuddySessionDir(path string) string {
	return transcriptCWD(path, func(string) bool { return true })
}

// IsWorkBuddyTranscript reports a transcript from WorkBuddy's store rather
// than CodeBuddy's.
func IsWorkBuddyTranscript(p string) bool {
	for _, d := range WorkBuddyConfigDirs() {
		if codeBuddyRelTo(filepath.Join(d, "projects"), p) != nil {
			return true
		}
	}
	return false
}

// IsCodeBuddyTranscript reports a CodeBuddy or WorkBuddy transcript, main or
// sub-agent, the way a hook payload's transcript_path names one.
// A transcript outside the configured roots (another DEJA_CODEBUDDY_ROOTS, a
// path through a symlink) is recognised by its first records instead.
func IsCodeBuddyTranscript(p string) bool {
	if p == "" {
		return false
	}
	if isCodeBuddySession(p) || CodeBuddySubagentFile(p) {
		return true
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, compactionHeaderBytes)
	n, _ := io.ReadFull(f, head)
	h, err := compactionHarness(nil, head[:n])
	return err == nil && h == "codebuddy"
}

// CodeBuddySubagentFile reports a sub-agent's transcript. Read the way the
// Claude and Qwen readers read theirs: task, edits and last turns, whole with
// DEJA_INCLUDE_SUBAGENTS=1, left out with =0 (#5301).
func CodeBuddySubagentFile(p string) bool { return codeBuddySubagentParts(codeBuddyRel(p)) }

// CodeBuddySessionFiles lists the transcripts.
func CodeBuddySessionFiles() []string {
	subagents := !SubagentsOff()
	var out []string
	for _, root := range CodeBuddyRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			parts := codeBuddyRelTo(root, p)
			return codeBuddyMainParts(parts) || subagents && codeBuddySubagentParts(parts)
		})...)
	}
	return out
}

// CodeBuddySidecarFiles are what CodeBuddy keeps beside its transcripts: the
// <id>.meta.json per session, the project's memory/ directory, and the
// per-session directory that holds sub-agents and their artifacts. doctor
// places them rather than counting them as transcripts it could not read; a
// file anywhere else under a project is still reported, which is how drift in
// the store shows up.
func CodeBuddySidecarFiles() []string {
	var out []string
	for _, root := range CodeBuddyRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			parts := codeBuddyRelTo(root, p)
			switch {
			case len(parts) < 2 || codeBuddySubagentParts(parts):
				return false
			case len(parts) == 2:
				return strings.HasSuffix(parts[1], ".meta.json")
			default:
				return true
			}
		})...)
	}
	return out
}

func LoadCodeBuddy() []model.Session {
	return parseFiles(CodeBuddySessionFiles(), ParseCodeBuddyFile)
}

// codeBuddyEnvelopes open the text the client puts under role user on its own
// account: slash-command echoes, injected reminders, local command output,
// teammate and task notifications.
var codeBuddyEnvelopes = []string{
	"<command-name>", "<command-message>", "<command-args>",
	"<system-reminder", "<local-command-", "<teammate-message",
	"<task-notification", "<agent-notification",
}

func codeBuddyEnvelope(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range codeBuddyEnvelopes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// codeBuddyPlumbing mirrors CodeBuddy's isRealUserMessageItem and
// isInternalContinuationItem: a record marked skipRun never went to the model
// as anyone's words (hook output, filter notices, local commands), and a user
// record marked as compaction, continuation or a teammate's message is the
// client talking to itself.
func codeBuddyPlumbing(m map[string]any) bool {
	pd, _ := m["providerData"].(map[string]any)
	if pd == nil {
		return false
	}
	for _, k := range []string{"skipRun", "isMeta", "isCompactInternal", "isCompacted", "isSummary"} {
		if v, _ := pd[k].(bool); v {
			return true
		}
	}
	if _, ok := pd["compactType"].(string); ok {
		return true
	}
	// The instruction prompt /compact sends is a user record marked only by
	// the agent that wrote it (#4704).
	if agent, _ := pd["agent"].(string); agent == "compact" {
		return true
	}
	if tm, ok := pd["teammateMessage"].(map[string]any); ok {
		if from, _ := tm["from"].(string); from != "" {
			return true
		}
	}
	return false
}

// codeBuddyText joins the text items of a message, dropping the ones that are
// an envelope rather than speech: a reminder rides in the same record as the
// prompt it was attached to. WorkBuddy's desktop app puts the reminders and
// the prompt in one item, the prompt last inside <user_query>; that item is
// the prompt.
func codeBuddyText(v any) string {
	if s, ok := v.(string); ok {
		if q, ok := UserQuery(s); ok {
			return q
		}
		if codeBuddyEnvelope(s) {
			return ""
		}
		return strings.TrimSpace(s)
	}
	items, _ := v.([]any)
	var parts []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		switch t, _ := m["type"].(string); t {
		case "input_text", "output_text", "text":
		default:
			continue
		}
		txt, _ := m["text"].(string)
		if q, ok := UserQuery(txt); ok {
			txt = q
		}
		if txt = strings.TrimSpace(txt); txt == "" || codeBuddyEnvelope(txt) {
			continue
		}
		parts = append(parts, txt)
	}
	return strings.Join(parts, "\n")
}

// codeBuddyOutput is a function_call_result's output: {type:"text", text}, a
// list of such items, or a bare string.
func codeBuddyOutput(v any) string {
	var s string
	switch o := v.(type) {
	case string:
		s = o
	case map[string]any:
		s, _ = o["text"].(string)
	case []any:
		s = contentText(o)
	}
	if codeBuddyImageRefs(s) {
		return ""
	}
	return s
}

// codeBuddyImageRefs reports the text a Read of an image leaves as its result:
// a JSON list of {type:"image_blob_ref", blob_id, mime, size, blob_path}. It is
// an image, not output, and every other reader drops image content.
func codeBuddyImageRefs(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.Contains(s, `"image_blob_ref"`) {
		return false
	}
	var items []map[string]any
	if json.Unmarshal([]byte(s), &items) != nil || len(items) == 0 {
		return false
	}
	for _, it := range items {
		if t, _ := it["type"].(string); t != "image_blob_ref" {
			return false
		}
	}
	return true
}

// codeBuddyExitCode reads how a shell call ended off its result text. The
// Bash builder always ends with an `Exit Code: N` line, `(none)` when a signal
// ended it; the other builder writes the line only for a non-zero code, so a
// `Command:` report with no such line and no signal, timeout or abort note
// after the output ran clean. Anything else is left unknown.
func codeBuddyExitCode(out string) (int, bool) {
	if !strings.HasPrefix(out, "Command: ") {
		return 0, false
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// From the end: the status lines follow the output, which may itself print
	// a line that looks like one.
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "Exit Code: ")
		if !ok {
			continue
		}
		// "Exit Code: 1 (no matches)" is a code the client took for no error;
		// it is still what the command returned.
		if j := strings.IndexByte(rest, ' '); j > 0 {
			rest = rest[:j]
		}
		return statusCode(rest, "", "")
	}
	// No line means clean only in a foreground report, which goes on with
	// Stdout. A backgrounded run (`Status: Running in background`) or one that
	// never launched (`Error: ...`) also opens with Command: and has no code.
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "Stdout: ") {
		return 0, false
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		switch {
		case line == "" || line == "Signal: (none)":
		case strings.HasPrefix(line, "Signal: "), strings.HasPrefix(line, "(command "):
			return 0, false
		default:
			return 0, true
		}
	}
	return 0, true
}

// codeBuddyRawExit reads the code off the structured result,
// providerData.toolResult.rawResponse. It is the only place the code survives
// when the output was too large for the transcript: the result then keeps a
// <persisted-output> preview of its first 2 KB, cut before the Exit Code line.
// known is false when the record has no such field; a run that was
// interrupted, killed by a signal or is still in the background is known and
// has no code.
func codeBuddyRawExit(m map[string]any) (code int, ok, known bool) {
	pd, _ := m["providerData"].(map[string]any)
	tr, _ := pd["toolResult"].(map[string]any)
	raw, _ := tr["rawResponse"].(map[string]any)
	v, has := raw["exitCode"]
	if !has {
		return 0, false, false
	}
	var n int
	switch x := v.(type) {
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0, false, true
		}
		n = int(i)
	case float64:
		if x != float64(int(x)) {
			return 0, false, true
		}
		n = int(x)
	default:
		return 0, false, true
	}
	if b, _ := raw["interrupted"].(bool); b {
		return 0, false, true
	}
	if sig, _ := raw["signal"].(string); sig != "" {
		return 0, false, true
	}
	return n, true, true
}

// ParseCodeBuddyFile reads one CodeBuddy or WorkBuddy transcript.
func ParseCodeBuddyFile(path string) ([]model.Session, error) {
	return parseCodeBuddy(path, func(fn func(map[string]any)) error { return scanJSONL(path, fn) })
}

// parseCodeBuddy is the reader over any source of records: the file, or the
// bounded copy a compaction hook reads.
func parseCodeBuddy(path string, scan func(func(map[string]any)) error) ([]model.Session, error) {
	s := model.Session{
		Harness: "codebuddy",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	projDir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		s.Kind = "subagent"
		s.Parent = filepath.Base(filepath.Dir(filepath.Dir(path)))
		projDir = filepath.Dir(filepath.Dir(filepath.Dir(path)))
	}
	cwd := ""
	// CodeBuddy's own title order: the newest custom title, then the newest
	// generated one, then the newest topic (getEffectiveSessionTitleItem).
	var custom, generated, topic string
	// The command's outcome is in the result that names its callId (#4703).
	exits := commandExits{}
	err := scan(func(m map[string]any) {
		t := parseTimeAny(m["timestamp"])
		if c, _ := m["cwd"].(string); c != "" && cwd == "" {
			cwd = c
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "custom-title":
			if v, _ := m["customTitle"].(string); strings.TrimSpace(v) != "" {
				custom = v
			}
			return
		case "ai-title":
			if v, _ := m["aiTitle"].(string); !codeBuddyPlaceholderTitle(v) {
				generated = v
			}
			return
		case "topic":
			if v, _ := m["topic"].(string); !codeBuddyPlaceholderTitle(v) {
				topic = v
			}
			return
		case "message", "function_call", "function_call_result":
		default:
			return
		}
		if codeBuddyPlumbing(m) {
			return
		}
		switch typ {
		case "message":
			role, _ := m["role"].(string)
			if role != "user" && role != "assistant" {
				return
			}
			if text := codeBuddyText(m["content"]); text != "" {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			}
		case "function_call":
			name, _ := m["name"].(string)
			id, _ := m["callId"].(string)
			in := map[string]any{}
			switch a := m["arguments"].(type) {
			case string:
				_ = json.Unmarshal([]byte(a), &in)
			case map[string]any:
				in = a
			}
			blocks := []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}}
			recs := codeBuddyWorkRecords(blocks, t)
			if len(recs) > 0 {
				from := len(s.Messages)
				s.Touch(t)
				s.Messages = append(s.Messages, recs...)
				if IndexCommands() && id != "" {
					exits.note(s.Messages, from, commandCallsIn(blocks, claudeDialect))
				}
			}
		case "function_call_result":
			if id, _ := m["callId"].(string); id != "" {
				if _, ok := exits[id]; ok {
					code, ok, known := codeBuddyRawExit(m)
					if !known {
						code, ok = codeBuddyExitCode(codeBuddyOutput(m["output"]))
					}
					if ok {
						exits.stamp(s.Messages, id, "", code)
					}
					delete(exits, id)
				}
			}
			if !IndexToolOutput() {
				return
			}
			if body := strings.TrimSpace(codeBuddyOutput(m["output"])); body != "" {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: t})
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	if cwd != "" {
		s.Project = projectName(cwd)
	} else {
		s.Project = claudeProjectName(projDir)
	}
	for _, title := range []string{custom, generated, topic} {
		if strings.TrimSpace(title) != "" {
			s.Title = firstLineTrim(title)
			break
		}
	}
	return []model.Session{s}, err
}

// codeBuddyPlaceholderTitle is isGeneratedPlaceholderTitle: titles CodeBuddy
// itself skips over.
func codeBuddyPlaceholderTitle(t string) bool {
	t = strings.TrimSpace(t)
	return t == "" || t == "(No content)" || t == "/compact" ||
		strings.HasPrefix(t, "<image_local_path>") && strings.HasSuffix(t, "</image_local_path>")
}

// codeBuddyWorkRecords is what one call leaves in the index: the files it
// named, the span an edit replaced, what it wrote and the command it ran.
func codeBuddyWorkRecords(blocks []any, ts time.Time) []model.Message {
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(blocks, claudeDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(blocks, claudeDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(blocks, claudeDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, claudeDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	return out
}
