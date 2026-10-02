package sources

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cline (github.com/cline/cline) has two session-store generations, both
// covered by one harness with two file kinds (issue #253):
//
//  1. The current CLI/SDK shared store:
//     ${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/
//     <sessionId>/<sessionId>.json          (manifest: cwd, timestamps, title)
//     <sessionId>/<sessionId>.messages.json (transcript)
//
//  2. The released VS Code extension's legacy store under the host's
//     globalStorage (saoudrizwan.claude-dev):
//     state/taskHistory.json                (task metadata list)
//     tasks/<taskId>/api_conversation_history.json (transcript)
//
// Both transcript formats are whole-file JSON rewritten on change, not
// append-only logs, so there is no incremental ParseFrom. Only user and
// assistant text blocks are indexed; tool payloads, thinking, files, images,
// subagents and compaction artifacts are skipped by design. Modern messages
// files are indexed only for the lead agent.

// ClineConfigDir is the native modern data root (~/.cline/data by default),
// following Cline's own precedence chain.
func ClineConfigDir() string {
	if p := os.Getenv("CLINE_DATA_DIR"); p != "" {
		return p
	}
	if p := os.Getenv("CLINE_DIR"); p != "" {
		return filepath.Join(p, "data")
	}
	return filepath.Join(Home(), ".cline", "data")
}

// ClineSessionsDir is the modern shared session store. DEJA_CLINE_ROOT
// relocates reads only, mirroring every other harness.
func ClineSessionsDir() string {
	if p := os.Getenv("DEJA_CLINE_ROOT"); p != "" {
		return p
	}
	if p := os.Getenv("CLINE_SESSION_DATA_DIR"); p != "" {
		return p
	}
	return filepath.Join(ClineConfigDir(), "sessions")
}

// ClineMCPSettingsPath is where `deja install cline` writes the MCP entry.
func ClineMCPSettingsPath() string {
	if p := os.Getenv("CLINE_MCP_SETTINGS_PATH"); p != "" {
		return p
	}
	return filepath.Join(ClineConfigDir(), "settings", "cline_mcp_settings.json")
}

// ClineLegacyRoots enumerates the VS Code-compatible hosts' extension
// global-storage directories. The extension has no native relocation
// variable, so DEJA_CLINE_ROOTS (a path list) is the read override.
func ClineLegacyRoots() []string {
	if list := os.Getenv("DEJA_CLINE_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	const ext = "saoudrizwan.claude-dev"
	var bases []string
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(Home(), "Library", "Application Support")
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(cfg, host, "User", "globalStorage", ext))
		}
	}
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

// ClineStoreRoots names every directory the transcript walk covers: the
// CLI/SDK sessions directory and each legacy extension root's tasks tree. Both
// doctor forms read it, so the row and the machine report cannot disagree on
// where a cline transcript is looked for (#3399).
func ClineStoreRoots() []string {
	roots := []string{ClineSessionsDir()}
	for _, root := range ClineLegacyRoots() {
		roots = append(roots, filepath.Join(root, "tasks"))
	}
	return roots
}

// ClineSessionFiles lists both generations' transcript files.
func ClineSessionFiles() []string {
	files := walkFiles(ClineSessionsDir(), func(p string) bool {
		return strings.HasSuffix(p, ".messages.json")
	})
	for _, root := range ClineLegacyRoots() {
		files = append(files, walkFiles(filepath.Join(root, "tasks"), func(p string) bool {
			return filepath.Base(p) == "api_conversation_history.json"
		})...)
	}
	return files
}

// ClineSidecarFiles lists the per-session manifest the reader opens itself for
// the title, the working directory and the timestamps. doctor counted one per
// session as a transcript it could not read, the same shape as #3297 (#3360).
func ClineSidecarFiles() []string {
	return walkFiles(ClineSessionsDir(), func(p string) bool {
		// The manifest is named after the directory it sits in, which is what
		// the reader opens; anything else under a session is a file deja has
		// no account of and the row should say so.
		return filepath.Base(p) == filepath.Base(filepath.Dir(p))+".json"
	})
}

func LoadCline() []model.Session {
	return parseFiles(ClineSessionFiles(), ParseClineFile)
}

// ParseClineFile dispatches on the file kind.
func ParseClineFile(path string) ([]model.Session, error) {
	if filepath.Base(path) == "api_conversation_history.json" {
		return parseClineLegacyTask(path)
	}
	return parseClineModernSession(path)
}

// --- modern CLI/SDK store ---

// ClineSessionDir is the directory a Cline CLI session ran in, from the
// <id>.json manifest beside its transcript: cwd, else workspace_root. "" when
// neither names an absolute path; a relative one would be read against
// wherever deja runs (#4318).
func ClineSessionDir(path string) string {
	sessionDir := filepath.Dir(path)
	b, err := os.ReadFile(filepath.Join(sessionDir, filepath.Base(sessionDir)+".json"))
	if err != nil {
		return ""
	}
	var man clineManifest
	if json.Unmarshal(b, &man) != nil {
		return ""
	}
	if d := firstNonEmpty(man.CWD, man.WorkspaceRoot); filepath.IsAbs(d) {
		return d
	}
	return ""
}

type clineManifest struct {
	SessionID     string `json:"session_id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	CWD           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
	Prompt        string `json:"prompt"`
	Metadata      struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

type clineMessages struct {
	Agent    string `json:"agent"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		TS      int64           `json:"ts"`
	} `json:"messages"`
}

func parseClineModernSession(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs clineMessages
	if err := json.Unmarshal(b, &msgs); err != nil {
		// The whole task is one document: nothing from this path is indexed,
		// which is a path deja could not read rather than a line it skipped.
		// "1 line could not be read" was said of three thousand turns (#2232).
		diagFileError(path, err)
		return nil, nil
	}
	// Only the lead agent's transcript is a user-facing session; subagent
	// and teammate message files share the format but not the meaning.
	if msgs.Agent != "" && msgs.Agent != "lead" {
		return nil, nil
	}
	sessionDir := filepath.Dir(path)
	id := filepath.Base(sessionDir)
	s := model.Session{Harness: "cline", ID: id, Path: path, Project: "cline"}
	var man clineManifest
	cwd := ""
	if mb, err := os.ReadFile(filepath.Join(sessionDir, id+".json")); err == nil {
		if json.Unmarshal(mb, &man) == nil {
			cwd = man.CWD
			if cwd == "" {
				cwd = man.WorkspaceRoot
			}
			if cwd != "" {
				s.Project = projectName(cwd)
			}
			s.Title = strings.TrimSpace(man.Metadata.Title)
			if s.Title == "" {
				s.Title = firstLineTrim(man.Prompt)
			}
			// The spec'd manifest uses created_at/updated_at; the shipped
			// CLI (3.0.46, observed live) writes started_at/ended_at.
			if t := parseTimeAny(firstNonEmpty(man.CreatedAt, man.StartedAt)); !t.IsZero() {
				s.Started = t
			}
			if t := parseTimeAny(firstNonEmpty(man.UpdatedAt, man.EndedAt)); !t.IsZero() {
				s.Updated = t
			}
		}
	}
	exits := commandExits{}
	for _, m := range msgs.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		ts := s.Started
		if m.TS > 0 {
			ts = time.UnixMilli(m.TS)
		}
		text := clineContentText(m.Content)
		if m.Role == "user" {
			text = unwrapClineTask(text)
		}
		if text != "" {
			s.Touch(ts)
			s.Messages = append(s.Messages, model.Message{Role: m.Role, Text: text, Time: ts})
		}
		// The work rides in the same content list, in blocks clineContentText
		// drops because they are not type:"text" — so the file an assistant
		// edited and the command it ran were reachable from nothing.
		from := len(s.Messages)
		if recs := clineWorkRecords(m.Content, cwd, ts); len(recs) > 0 {
			s.Touch(ts)
			s.Messages = append(s.Messages, recs...)
		}
		clineJoinExits(s.Messages, from, m.Content, clineDialect, exits)
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// --- legacy VS Code extension store ---

type clineTaskMeta struct {
	ID   string `json:"id"`
	TS   int64  `json:"ts"`
	Task string `json:"task"`
	CWD  string `json:"cwdOnTaskInitialization"`
}

func parseClineLegacyTask(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var turns []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &turns); err != nil {
		diagFileError(path, err)
		return nil, nil
	}
	taskDir := filepath.Dir(path)
	taskID := filepath.Base(taskDir)
	root := filepath.Dir(filepath.Dir(taskDir))
	s := model.Session{Harness: "cline", ID: "cline-task-" + taskID, Path: path, Project: "cline"}
	base := time.Time{}
	workspace := ""
	if hb, err := os.ReadFile(filepath.Join(root, "state", "taskHistory.json")); err == nil {
		var metas []clineTaskMeta
		if json.Unmarshal(hb, &metas) == nil {
			for _, m := range metas {
				if m.ID == taskID {
					s.Title = firstLineTrim(m.Task)
					if m.CWD != "" {
						workspace = m.CWD
						s.Project = projectName(m.CWD)
					}
					if m.TS > 0 {
						base = time.UnixMilli(m.TS)
					}
					break
				}
			}
		}
	}
	if base.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			base = fi.ModTime()
		}
	}
	contents := make([]json.RawMessage, len(turns))
	for i, m := range turns {
		contents[i] = m.Content
	}
	xmlEra := rooXMLEra(contents)
	exits := commandExits{}
	for ti, m := range turns {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		ts := base.Add(time.Duration(ti) * time.Second)
		text := clineContentText(m.Content)
		if m.Role == "user" {
			// A result of the XML era is a text block, not a tool_result
			// (#4424), so the person's words are what is left beside it.
			results, words := rooUserTurn(m.Content, xmlEra)
			tool := append(clineTurnToolOutput(m.Content, ts), rooLegacyToolOutput(results, ts)...)
			if len(tool) > 0 {
				s.Touch(ts)
				s.Messages = append(s.Messages, tool...)
			}
			clineJoinExits(s.Messages, len(s.Messages), m.Content, rooDialect, exits)
			text = unwrapClineTask(words)
		} else if work := rooWorkRecords(m.Content, ts, workspace, xmlEra); len(work) > 0 {
			s.Touch(ts)
			from := len(s.Messages)
			s.Messages = append(s.Messages, work...)
			clineJoinExits(s.Messages, from, m.Content, rooDialect, exits)
		}
		if text == "" {
			continue
		}
		s.Touch(ts)
		s.Messages = append(s.Messages, model.Message{Role: m.Role, Text: text, Time: ts})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// clineDialect is Cline's tool vocabulary, read off the schemas its own CLI
// declares. Three of them differ from every other harness: `run_commands`
// takes a list under `commands` rather than one string, `read_files` takes a
// list of read requests under `files`, and the editor names the replaced text
// `old_text` and the written text `new_text` — the only record of a file the
// editor created, which was read under new_string and lost (#4503).
// apply_patch takes its patch under `input`; clineWorkRecords reads it.
var clineDialect = toolDialect{
	pathKey:     "path",
	pathListKey: "files",
	pathTools:   map[string]bool{"editor": true, "read_files": true},
	shellTool:   "run_commands",
	commandKey:  "commands",
	editTools:   map[string]bool{"editor": true},
	oldKey:      "old_text",
	newKey:      "new_text",
}

// rooDialect is what the Roo Code and the legacy Cline extension call their
// tools: execute_command with `command`, and `path` on the file tools —
// read_file, write_to_file, apply_diff, insert_content, search_and_replace,
// replace_in_file. Neither reader emitted a call as a work record before
// #3295. The two sides of an edit come out of rooEditRecords rather than the
// shared helper: apply_diff carries a SEARCH/REPLACE block, not an
// old_string. Current Roo adds search_replace, edit_file and edit, which name
// the file `file_path`, and apply_patch, whose paths are in the patch body
// (#4419).
var rooDialect = toolDialect{
	pathKey:    "path",
	pathKeyAlt: "file_path",
	pathTools: map[string]bool{"read_file": true, "write_to_file": true, "apply_diff": true,
		"insert_content": true, "search_and_replace": true, "replace_in_file": true,
		"search_replace": true, "edit_file": true, "edit": true},
	shellTool: "execute_command",
	editTools: map[string]bool{},
}

// rooWorkRecords is clineWorkRecords for the task files: the command a call
// ran and the files it named, under the same switches.
func rooWorkRecords(raw json.RawMessage, ts time.Time, workspace string, xmlEra bool) []model.Message {
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		blocks = []any{map[string]any{"type": "text", "text": s}}
	}
	if xmlEra {
		blocks = rooWithXMLCalls(blocks)
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := rooResolvePaths(rooPatchPaths(blocks, toolPathsIn(blocks, rooDialect)), workspace); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() || IndexEdits() {
		spans, wrote := rooEditRecords(blocks, workspace)
		if IndexWrites() {
			for _, w := range wrote {
				out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
			}
		}
		if IndexEdits() {
			for _, span := range spans {
				out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
			}
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, rooDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	return out
}

// clineWorkRecords turns the tool blocks of one message into work records.
// cwd is where the session ran: a patch names its files relative to it.
func clineWorkRecords(raw json.RawMessage, cwd string, ts time.Time) []model.Message {
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(blocks, clineDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(blocks, clineDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(blocks, clineDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
		}
	}
	for _, patch := range applyPatchInputs(blocks, clineDialect) {
		out = append(out, applyPatchRecords(patch, func(p string) string { return resolveToolPath(p, cwd) }, ts)...)
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, clineDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	if IndexToolOutput() {
		for _, body := range clineToolResults(blocks) {
			out = append(out, model.Message{Role: RoleToolOutput, Text: body, Time: ts})
		}
	}
	return out
}

// clineJoinExits notes the commands a message's tool calls appended from
// index from on, and stamps those its tool results report on (#4502). A
// command is emitted from its tool_use and its status sits in the tool_result
// of a later message, and nothing joined the two.
func clineJoinExits(msgs []model.Message, from int, raw json.RawMessage, d toolDialect, exits commandExits) {
	if !IndexCommands() || !bytes.Contains(raw, []byte(`"tool_`)) {
		return
	}
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	exits.note(msgs, from, commandCallsIn(blocks, d))
	for _, it := range blocks {
		m, ok := it.(map[string]any)
		if !ok || m["type"] != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if _, ok := exits[id]; !ok {
			continue
		}
		// Cline CLI: one {query, error, success} entry per command of a
		// run_commands batch; success is a clean exit, and a non-zero one is
		// the error "Command exited with code N".
		if list, ok := m["content"].([]any); ok {
			for _, e := range list {
				entry, _ := e.(map[string]any)
				query, _ := entry["query"].(string)
				if ok, _ := entry["success"].(bool); ok {
					exits.stamp(msgs, id, query, 0)
				} else if er, _ := entry["error"].(string); er != "" {
					if code, ok := statusCode(er, "Command exited with code ", ""); ok {
						exits.stamp(msgs, id, query, code)
					}
				}
			}
		}
		// The VS Code extension's execute_command opens its result with the
		// status (CommandOrchestrator.ts).
		line := firstLine(contentText(m["content"]))
		if code, ok := statusCode(line, "Command failed with exit code ", "."); ok {
			exits.stamp(msgs, id, "", code)
		} else if code, ok := statusCode(line, "Command executed successfully (exit code ", ")."); ok {
			exits.stamp(msgs, id, "", code)
		}
	}
}

// clineTurnToolOutput is what a turn's tool_result blocks printed, for the
// legacy Cline and the Roo task files, which put a command's output there and
// the person's words (if any) in text blocks beside it. Read as text blocks
// only, a failing command's error reached neither search nor the fix pairs
// (#3269). The same switch and the same role the modern reader uses.
func clineTurnToolOutput(raw json.RawMessage, ts time.Time) []model.Message {
	if !IndexToolOutput() {
		return nil
	}
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []model.Message
	for _, body := range clineToolResults(blocks) {
		out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: ts})
	}
	return out
}

// clineToolResults reads what a call printed. The content is a string, text
// blocks, or Cline CLI's list of per-command entries, and results are kept
// whether or not the call succeeded — the error a command hit is what a later
// search reaches for.
func clineToolResults(blocks []any) []string {
	var out []string
	for _, it := range blocks {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "tool_result" {
			continue
		}
		body := strings.TrimSpace(contentText(m["content"]))
		if body == "" {
			body = clineResultEntries(m["content"])
		}
		if body != "" {
			out = append(out, body)
		}
	}
	return out
}

// clineResultEntries reads the list Cline CLI writes for run_commands and
// read_files: one {query, result, error, success} entry per command or file.
// The result carries the stderr; the error is added when the result does not
// already say it, and stands alone when the command never started (#4315).
func clineResultEntries(v any) string {
	list, _ := v.([]any)
	var parts []string
	for _, it := range list {
		e, ok := it.(map[string]any)
		if !ok {
			continue
		}
		r, _ := e["result"].(string)
		r = strings.TrimSpace(r)
		// The error is usually already in the result ("[Command exited with
		// code 1]"); one that says something else, like a timeout, is kept too.
		if er, _ := e["error"].(string); strings.TrimSpace(er) != "" && !strings.Contains(r, strings.TrimSpace(er)) {
			r = strings.TrimSpace(r + "\n" + strings.TrimSpace(er))
		}
		if r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, "\n")
}

// clineContentText extracts plain text from either a string content or a
// typed block list, keeping only type:"text" blocks.
func clineContentText(raw json.RawMessage) string {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, blk := range blocks {
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			parts = append(parts, strings.TrimSpace(blk.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// unwrapClineTask strips the legacy <task>...</task> envelope (and its modern
// user-input equivalent) so the tags themselves are not indexed, and the
// host's <environment_details> block, which is not the person's words.
func unwrapClineTask(text string) string {
	t := stripNoToolsPrompt(stripClineHostBlocks(text))
	for _, tag := range []string{"task", "user_message", "user_input"} {
		open := "<" + tag
		if !strings.HasPrefix(t, open) {
			continue
		}
		rest := t[len(open):]
		// The live CLI writes attributes: <user_input mode="act">.
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			return t
		}
		rest = rest[gt+1:]
		if i := strings.Index(rest, "</"+tag+">"); i >= 0 {
			rest = rest[:i] + rest[i+len("</"+tag+">"):]
		}
		return strings.TrimSpace(rest)
	}
	return t
}

// The retry prompt Roo and Cline send as a user turn when the model answered
// without a tool call (formatResponse.noToolsUsed). The client shows it as an
// error row, not as something the person typed, and on one live Roo task it
// was 36 of 37 user turns (#4421).
const (
	noToolsPromptHead = "[ERROR] You did not use a tool in your previous response!"
	noToolsPromptTail = "(This is an automated message, so do not respond to it conversationally.)"
)

// stripNoToolsPrompt drops that prompt from a turn's text. It starts a line
// when the client writes it, so a person quoting it in a sentence keeps it; a
// prompt whose closing line is missing runs to the end of the text.
func stripNoToolsPrompt(text string) string {
	for {
		i := strings.Index(text, noToolsPromptHead)
		for i > 0 && text[i-1] != '\n' {
			j := strings.Index(text[i+1:], noToolsPromptHead)
			if j < 0 {
				return text
			}
			i += 1 + j
		}
		if i < 0 {
			return text
		}
		end := len(text)
		if k := strings.Index(text[i:], noToolsPromptTail); k >= 0 {
			end = i + k + len(noToolsPromptTail)
		}
		text = strings.TrimSpace(text[:i] + text[end:])
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// pathToProjectKey converts an absolute workspace path to the dash-encoded
// key claudeProjectName expects. A reader that has the path itself labels it
// with projectName instead: decoding the key back guesses between my-app and
// my/app (#4458).
func pathToProjectKey(p string) string {
	// A Windows path folds the same way: backslashes are separators too, and
	// the drive letter's colon is not a character a key carries (#3217).
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(p)
}

func firstLineTrim(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	// Rune-safe: a prompt in any non-ASCII language was cut mid-character and
	// the broken byte went into the stored title, which every surface shows
	// (#1319).
	return truncateRunes(s, 120)
}

// ClinePluginsDir is where the CLI looks for plugins. Note it is not under
// the data directory: plugins sit next to it, and CLINE_DIR moves both.
func ClinePluginsDir() string {
	if p := os.Getenv("CLINE_DIR"); p != "" {
		return filepath.Join(p, "plugins")
	}
	return filepath.Join(Home(), ".cline", "plugins")
}

var clineHostBlocks = []string{"environment_details", "workspace_diagnostics", "slash_command"}

func stripClineHostBlocks(text string) string {
	t := text
	for _, tag := range clineHostBlocks {
		// Line-anchored: the host writes the block on its own line, and a
		// person naming the tag inside a sentence — "why is
		// <environment_details> in my history" — is asking about it, not
		// pasting one (#3255).
		open := regexp.MustCompile(`(?m)^[ \t]*<` + tag + `>[ \t]*\r?$`)
		closeTag := "</" + tag + ">"
		for {
			loc := open.FindStringIndex(t)
			if loc == nil {
				break
			}
			rest := t[loc[1]:]
			k := strings.Index(rest, closeTag)
			if k < 0 {
				// An unterminated block runs to the end of the message: the
				// listing was cut mid-write, and what follows is not the
				// person's either.
				t = t[:loc[0]]
				break
			}
			end := loc[1] + k + len(closeTag)
			t = t[:loc[0]] + strings.TrimPrefix(t[end:], "\n")
		}
	}
	return strings.TrimSpace(t)
}
