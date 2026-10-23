package sources

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// MiniMax Code (MCode) is the desktop agent. Its local runtime keeps one
// directory per conversation under
//
//	~/.minimax/v2/sessions/YYYY/MM/DD/HH-mm-ss-mmm-session_<id>/
//
// and writes the transcript beside a manifest.json:
//
//	messages.jsonl  one JSON object per line
//	manifest.json   the session id, its clock, and the paths of the layout
//
// Every line is one message:
//
//	{"message_id":…,"turn_id":…,"message":{"role":…,"content":[…],"timestamp":…}}
//
// The role is user, assistant or toolResult. A content block is text, thinking
// or toolCall — {type, id, name, arguments} — and a tool result carries the
// toolCallId and toolName it answers.
//
// A fourth role, compactionSummary, is what a compaction leaves behind: the
// runtime replaces the turns it cut with a summary of them and keeps those
// turns in its own snapshots, so the summary is the only record of them the
// transcript holds. It is indexed under the summary role, not as speech.
//
// A person's turn does not hold only what the person wrote. The runtime
// assembles the turn around it: the agent banner, the permission notice, the
// skill catalogue, the media reminder. genuineUserQueryText or
// canonicalTextRange says which part of that text was typed (mcodeUserText),
// and indexing the rest would put the harness's own words at the head of every
// session on the machine and in every answer about them. A user-role line
// carrying archonCompaction is a compaction marker, not a turn.
//
// The thinking block is the model reasoning out loud, not what it told the
// person, so it stays out of the transcript the way it does in every other
// harness here.
//
// The transcript holds no working directory. The runtime keeps it in its own
// SQLite store (McodeSessionWorkspace), read through sqlite3; without that,
// the project comes from where the session's own file activity was, the
// evidence the other harnesses without a recorded cwd are filed under (#542).
//
// Read off MiniMax Code's local runtime v2 sessions, 2026-10-20; user text and
// compaction markers checked against the @minimax-ai/code 0.6.12 bundle.

// McodeConfigHome is MCode's own data directory, where its MCP configuration
// lives beside the sessions.
func McodeConfigHome() string { return filepath.Join(Home(), ".minimax") }

// McodeRoot is the session store, overridable via DEJA_MCODE_ROOT.
func McodeRoot() string {
	return EnvPath("DEJA_MCODE_ROOT", filepath.Join(McodeConfigHome(), "v2", "sessions"))
}

// mcodeTranscriptName is the transcript the runtime writes beside a session
// directory's manifest.
const mcodeTranscriptName = "messages.jsonl"

// McodeSessionFiles lists the transcripts under the session root.
// A .history-mutation-<id> directory inside a session is the runtime staging
// a rewrite of the history (RSe); a copy left behind is the same session, not
// another one.
func McodeSessionFiles() []string {
	return walkFiles(McodeRoot(), mcodeIsTranscript)
}

func mcodeIsTranscript(p string) bool {
	return filepath.Base(p) == mcodeTranscriptName &&
		strings.HasPrefix(p, McodeRoot()+string(filepath.Separator)) &&
		!mcodeStaged(p)
}

func mcodeStaged(p string) bool {
	return strings.HasPrefix(filepath.Base(filepath.Dir(p)), ".history-mutation-")
}

// McodeSidecarFiles lists the runtime's own bookkeeping beside each
// transcript: the session's manifest, the catalogue of its history, the record
// of the last model call and the locators of what the person typed. Under the
// session's own directories sit the rest of the runtime's work: the snapshots
// directory holds the environment and the task list the session was working
// from, and the reports directory holds the tool output too large to keep
// inline. None of them is a transcript, and a row that counted them as files
// deja could not read would send someone looking for a fault that is not there.
func McodeSidecarFiles() []string {
	names := map[string]bool{
		"manifest.json":               true,
		"history-catalog.json":        true,
		"llm-call.json":               true,
		"user-message-locators.jsonl": true,
	}
	return walkFiles(McodeRoot(), func(p string) bool {
		if !strings.HasPrefix(p, McodeRoot()+string(filepath.Separator)) {
			return false
		}
		if names[filepath.Base(p)] || mcodeStaged(p) {
			return true
		}
		return mcodeUnderDir(p, "snapshots") || mcodeUnderDir(p, "reports")
	})
}

// mcodeUnderDir reports whether the path sits under one of the runtime's own
// directories, at whatever depth below it the file is. The runtime writes the
// reports at a depth of its own choosing, so the whole tree counts.
func mcodeUnderDir(p, dir string) bool {
	root := McodeRoot()
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if filepath.Base(d) == dir {
			return true
		}
		if d == root {
			return false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
	}
}

// LoadMcode loads every MCode session.
func LoadMcode() []model.Session { return parseFiles(McodeSessionFiles(), ParseMcodeFile) }

// ParseMcodeFile reads one session transcript.
func ParseMcodeFile(path string) ([]model.Session, error) {
	return parseMcodeFile(path, 0)
}

// ParseMcodeFileFromOffset reads a transcript from a byte offset, for the
// append path: the runtime only ever adds lines to the end of one.
func ParseMcodeFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseMcodeFile(path, offset)
}

func parseMcodeFile(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "mcode",
		ID:      mcodeSessionID(path),
		Project: "-",
		Path:    path,
	}
	// The title is what the person said first, and that line is behind the
	// offset on a resumed read: it is read again for the title alone.
	if offset > 0 {
		if head := leadingJSONLHeader(path, offset, headerLookahead, isMcodeTurn); head != nil {
			mcodeApplyUserTitle(&s, head)
		}
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		msg, _ := m["message"].(map[string]any)
		if msg == nil {
			return
		}
		role, _ := msg["role"].(string)
		switch role {
		case "user":
			if mcodeIsCompactionMarker(msg) {
				mcodeApplyArchon(&s, msg)
				return
			}
			mcodeApplyUser(&s, m)
		case "assistant":
			mcodeApplyAssistant(&s, msg)
		case "compactionSummary":
			mcodeApplySummary(&s, msg)
		case "toolResult":
			t := parseTimeAny(msg["timestamp"])
			text := mcodeText(mcodeWithoutHookBlocks(msg["content"]))
			if text == "" || !IndexToolOutput() {
				return
			}
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{
				Role: RoleToolOutput, Text: capParsedMessage(text), Time: t,
			})
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	if ws := mcodeSessionProject(s.ID); ws != "" {
		s.Project = projectName(ws)
	} else if p := projectFromPaths(s.Messages); p != "" {
		s.Project = p
	} else {
		s.ProjectFromDir = true
	}
	return []model.Session{s}, err
}

// McodeStateDB is the runtime's own SQLite store, beside the sessions
// directory: ~/.minimax/v2/sqlite/runtime-state.sqlite.
func McodeStateDB() string {
	return filepath.Join(filepath.Dir(McodeRoot()), "sqlite", "runtime-state.sqlite")
}

// mcodeWorkspaceCache is the session-to-workspace table, read once per
// change of the database rather than once per transcript.
var mcodeWorkspaceCache struct {
	sync.Mutex
	key  string
	dirs map[string]mcodeDirs
}

// mcodeDirs is where one session ran: workspace is the directory its tools ran
// in (a worktree, for a worktree session), project the repository it belongs
// to.
type mcodeDirs struct{ workspace, project string }

// McodeSessionWorkspace is the directory the session ran in, the cwd its hooks
// are given. The transcript and the manifest record none; the runtime keeps
// it in local_runtime_sessions.workspace_dir. "" when the database or sqlite3
// is not there, or when the session ran in the runtime's default workspace,
// which is its own scratch directory and no project.
func McodeSessionWorkspace(id string) string {
	return mcodeWorkspaces()[id].workspace
}

// mcodeSessionProject is the repository the session belongs to:
// project_workspace_dir, which for a worktree session is the parent checkout,
// else the workspace.
func mcodeSessionProject(id string) string {
	d := mcodeWorkspaces()[id]
	if d.project != "" {
		return d.project
	}
	return d.workspace
}

func mcodeWorkspaces() map[string]mcodeDirs {
	db := McodeStateDB()
	info, err := os.Stat(db)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	// The runtime writes through a WAL, so the main file's clock alone can
	// stay put while sessions are added.
	key := fmt.Sprintf("%s|%d|%d", db, info.ModTime().UnixNano(), info.Size())
	if wal, werr := os.Stat(db + "-wal"); werr == nil {
		key += fmt.Sprintf("|%d|%d", wal.ModTime().UnixNano(), wal.Size())
	}
	c := &mcodeWorkspaceCache
	c.Lock()
	defer c.Unlock()
	if c.key == key {
		return c.dirs
	}
	c.key, c.dirs = key, nil
	if !SQLite3Available() {
		return nil
	}
	out, err := sqliteOutput(db, "select json_object('id', session_id, "+
		"'ws', coalesce(workspace_dir, ''), 'proj', coalesce(project_workspace_dir, '')) "+
		"from local_runtime_sessions where is_default_workspace = 0;")
	if err != nil {
		return nil
	}
	rows, err := sqliteObjects[struct {
		ID   string `json:"id"`
		WS   string `json:"ws"`
		Proj string `json:"proj"`
	}](out)
	if err != nil {
		return nil
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return ""
	}
	dirs := make(map[string]mcodeDirs, len(rows))
	for _, r := range rows {
		if d := (mcodeDirs{abs(r.WS), abs(r.Proj)}); r.ID != "" && d.workspace != "" {
			dirs[r.ID] = d
		}
	}
	c.dirs = dirs
	return dirs
}

// mcodeDialect is MCode's tool vocabulary: bash takes `command`, read and
// write take `path`, and edit takes `file_path` with old_string and
// new_string. The search tools take a pattern and a directory, so they name
// no file the session touched and are left out of the path tools.
var mcodeDialect = toolDialect{
	pathKey:    "path",
	pathKeyAlt: "file_path",
	pathTools: map[string]bool{
		"read": true, "write": true, "edit": true, "apply_patch": true,
		"read_file": true, "write_file": true, "edit_file": true,
		"notebook_read": true, "notebook_edit": true,
	},
	shellTools: map[string]bool{"bash": true, "shell": true, "pwsh": true},
	editTools: map[string]bool{
		"write": true, "edit": true, "apply_patch": true,
		"write_file": true, "edit_file": true, "notebook_edit": true,
	},
}

// mcodeSessionID is the id of the session a transcript belongs to. The
// directory name holds it base64-encoded; the manifest beside the transcript
// holds it plain, and is read first because it is the runtime's own record.
func mcodeSessionID(path string) string {
	dir := filepath.Dir(path)
	if id := mcodeManifestID(dir); id != "" {
		return id
	}
	name := filepath.Base(dir)
	i := strings.LastIndex(name, "session_")
	if i < 0 {
		return name
	}
	enc := name[i+len("session_"):]
	if id, ok := decodeMcodeID(enc); ok {
		return id
	}
	return enc
}

// mcodeManifestID is the session id the runtime's manifest records, or "" when
// the manifest is missing or unreadable.
func mcodeManifestID(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return ""
	}
	var m struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.SessionID
}

// decodeMcodeID decodes the id the directory name carries: base64url, no
// padding, cut at 180 characters (JDe). Standard base64 is tried too.
func decodeMcodeID(enc string) (string, bool) {
	enc = strings.TrimRight(enc, "=")
	for _, dec := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		raw, err := dec.DecodeString(enc)
		if err != nil {
			continue
		}
		if id := string(raw); utf8.ValidString(id) && strings.TrimSpace(id) != "" {
			return id, true
		}
	}
	return "", false
}

// isMcodeTurn reports whether a line is a message the runtime wrote, which is
// what the header read on a resumed parse looks for.
func isMcodeTurn(m map[string]any) bool {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return false
	}
	if mcodeIsCompactionMarker(msg) {
		return false
	}
	role, _ := msg["role"].(string)
	return role == "user" || role == "assistant" || role == "toolResult"
}

// mcodeIsCompactionMarker reports whether a user-role line is a compaction
// marker rather than a turn: archonCompaction is set to a truthy value, the
// runtime's own test (J4n).
func mcodeIsCompactionMarker(msg map[string]any) bool {
	switch v := msg["archonCompaction"].(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	case float64:
		return v != 0
	}
	return true
}

// mcodeApplyUser folds a person's turn into the session: the text they typed,
// the title it gives the session, and the clock it stamps.
func mcodeApplyUser(s *model.Session, m map[string]any) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return
	}
	text := mcodeUserText(msg)
	if text == "" {
		return
	}
	t := parseTimeAny(msg["timestamp"])
	s.Touch(t)
	if s.Title == "" {
		s.Title = firstLineTrim(text)
	}
	s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
}

// mcodeApplySummary folds a compaction summary into the session. The runtime
// writes it where the turns it replaced used to be, and keeps those turns in
// its own snapshots: the summary is the only record of them in the
// transcript, so it is indexed — under the summary role, which is what keeps
// it from reading as speech nobody said (#4801).
func mcodeApplySummary(s *model.Session, msg map[string]any) {
	text, _ := msg["summary"].(string)
	if strings.TrimSpace(text) == "" {
		return
	}
	t := parseTimeAny(msg["timestamp"])
	s.Touch(t)
	s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: text, Time: t})
}

// mcodeApplyArchon reads a compaction marker: a user-role line the runtime
// writes at the head of the history it kept, carrying archonCompaction. It is
// not a turn the person typed. Its summary, when it has one, is the record of
// the turns the compaction cut, and is indexed the way a compactionSummary
// line is. The marker a migration leaves when it could not recover the older
// history (archonLegacyContextBoundary) says only that, and is skipped.
func mcodeApplyArchon(s *model.Session, msg map[string]any) {
	if _, ok := msg["archonLegacyContextBoundary"]; ok {
		return
	}
	ac, _ := msg["archonCompaction"].(map[string]any)
	text, _ := ac["summary"].(string)
	if strings.TrimSpace(text) == "" {
		return
	}
	t := parseTimeAny(msg["timestamp"])
	s.Touch(t)
	s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: text, Time: t})
}

// mcodeApplyUserTitle takes only the title from a turn, for the line a
// resumed read reads again. The turn itself is already in the index, and
// adding it a second time would show the person their own question twice.
func mcodeApplyUserTitle(s *model.Session, m map[string]any) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil || mcodeIsCompactionMarker(msg) {
		return
	}
	text := mcodeUserText(msg)
	if text == "" {
		return
	}
	if s.Title == "" {
		s.Title = firstLineTrim(text)
	}
}

// mcodeUserText is what the person wrote in this turn, read the way the
// runtime reads it back (J4n in @minimax-ai/code 0.6.12):
//
//  1. genuineUserQueryText, when the turn carries it, is the typed text. The
//     runtime writes it, with an empty canonicalTextRange, whenever the typed
//     text is not the tail of what it assembled around it.
//  2. canonicalTextRange marks the typed text inside every text block joined.
//  3. A turn with neither is the assembled text with the runtime's
//     system-reminder and background-task-finished blocks taken out; a
//     scheduled task's own prompt is no one's words.
//
// A compaction marker (archonCompaction) is not a turn at all; the caller
// handles it before this.
func mcodeUserText(msg map[string]any) string {
	if v, ok := msg["genuineUserQueryText"]; ok {
		text, _ := v.(string)
		return strings.TrimSpace(text)
	}
	joined := mcodeJoinedText(msg["content"])
	if ctr, ok := msg["canonicalTextRange"].(map[string]any); ok {
		if cut, ok := mcodeCanonicalText(joined, ctr); ok {
			return strings.TrimSpace(cut)
		}
		// A range that does not fit the text: the runtime treats the whole
		// history as unreadable; deja keeps this turn with the runtime's
		// blocks taken out.
	}
	text := mcodeWithoutHookContext(mcodeStripRuntimeBlocks(joined))
	if mcodeCronPrompt.MatchString(text) {
		return ""
	}
	return text
}

// mcodeRuntimeBlocks are the blocks the runtime wraps around a turn: the
// reminders it adds and the notice that a background task finished. The
// patterns are the runtime's own (AUt), case-insensitive, with the
// self-closing form too.
var mcodeRuntimeBlocks = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<system-reminder\b[^>]*>.*?</system-reminder>`),
	regexp.MustCompile(`(?is)<background-task-finished\b[^>]*>.*?</background-task-finished>`),
	regexp.MustCompile(`(?i)<(?:system-reminder|background-task-finished)\b[^>]*/>`),
}

// mcodeCronPrompt is the head of a turn a scheduled task sent rather than a
// person (RUt).
var mcodeCronPrompt = regexp.MustCompile(`(?i)^\[(?:System CronTask|Cron Report\b)[^\]]*\]`)

// mcodeHookContext is what plugin hooks answered. The runtime appends it as
// "<plugin-hook-context>\n…\n</plugin-hook-context>": as the last text block
// of a tool result, and after "\n\n" at the end of a turn's text. With deja
// installed that is deja's own recall; indexed, it would come back as the
// session's words the next time (#4801). Only that trailing shape is taken
// out, so output that merely mentions the tag keeps it.
const (
	mcodeHookOpen  = "<plugin-hook-context>\n"
	mcodeHookClose = "\n</plugin-hook-context>"
)

func mcodeIsHookContext(text string) bool {
	return strings.HasPrefix(text, mcodeHookOpen) && strings.HasSuffix(text, mcodeHookClose)
}

// mcodeWithoutHookBlocks drops the text blocks that are a hook's answer.
func mcodeWithoutHookBlocks(v any) any {
	blocks, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if text, _ := m["text"].(string); m["type"] == "text" && mcodeIsHookContext(text) {
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

func mcodeWithoutHookContext(text string) string {
	t := strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(t, mcodeHookClose) {
		return text
	}
	i := strings.LastIndex(t, "\n\n"+mcodeHookOpen)
	if i < 0 {
		return text
	}
	return strings.TrimSpace(t[:i])
}

func mcodeStripRuntimeBlocks(text string) string {
	for _, re := range mcodeRuntimeBlocks {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// mcodeJoinedText is the turn's text blocks joined with a newline, untrimmed:
// the string canonicalTextRange counts into (YIe). A content that is a plain
// string is that string.
func mcodeJoinedText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	blocks, ok := v.([]any)
	if !ok {
		return ""
	}
	var out []string
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "text" {
			continue
		}
		if text, ok := m["text"].(string); ok {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

// mcodeCanonicalText cuts the text the person typed out of the text the
// runtime assembled around it. The offsets are the ones a JavaScript string
// reports — UTF-16 code units — because the runtime is one, and a Chinese
// character is one unit there and three bytes here.
func mcodeCanonicalText(text string, ctr map[string]any) (string, bool) {
	start, ok := mcodeOffset(ctr["startOffset"])
	if !ok || start < 0 {
		return "", false
	}
	end, ok := mcodeOffset(ctr["endOffset"])
	if !ok || end < start {
		return "", false
	}
	units := utf16.Encode([]rune(text))
	if end > len(units) {
		return "", false
	}
	return string(utf16.Decode(units[start:end])), true
}

// mcodeOffset reads one of the range's offsets, whatever number the decoder
// handed back.
func mcodeOffset(v any) (int, bool) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n), true
		}
		// 3266.0 and 1e3 are integers to the runtime too.
		f, err := x.Float64()
		if err != nil || f != float64(int64(f)) {
			return 0, false
		}
		return int(f), true
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	}
	return 0, false
}

// mcodeApplyAssistant folds the agent's turn into the session: what it said
// and the work its tool calls stand for.
func mcodeApplyAssistant(s *model.Session, msg map[string]any) {
	t := parseTimeAny(msg["timestamp"])
	if text := mcodeText(msg["content"]); text != "" {
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{
			Role: "assistant", Text: capParsedMessage(text), Time: t,
		})
	}
	calls := mcodeToolCalls(msg["content"])
	if len(calls) == 0 {
		return
	}
	s.Touch(t)
	if IndexToolPaths() {
		if p := toolPathsIn(calls, mcodeDialect); p != "" {
			s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, mcodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, mcodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(calls, mcodeDialect) {
			s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
}

// mcodeText is what the turn said, out loud: the text blocks, and nothing
// else. The thinking block is the model reasoning to itself.
func mcodeText(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		return ""
	}
	var out []string
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "text" {
			continue
		}
		if text, _ := m["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// mcodeToolCalls puts the calls in the tool_use shape the shared extractors
// read.
func mcodeToolCalls(v any) []any {
	blocks, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []any
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "toolCall" {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		in, _ := m["arguments"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		id, _ := m["id"].(string)
		out = append(out, map[string]any{"type": "tool_use", "id": id, "name": name, "input": in})
	}
	return out
}

// McodeSessionPath is the transcript of the session with this id, or "" when
// the store holds none. The directory name ends in session_ and the id in
// base64url cut at 180 characters (JDe); the id itself is checked against
// the manifest, so a cut name never stands in for another session.
func McodeSessionPath(id string) string {
	if id == "" {
		return ""
	}
	enc := base64.RawURLEncoding.EncodeToString([]byte(id))
	if len(enc) > 180 {
		enc = enc[:180]
	}
	for _, p := range McodeSessionFiles() {
		if strings.HasSuffix(filepath.Base(filepath.Dir(p)), "session_"+enc) && mcodeSessionID(p) == id {
			return p
		}
	}
	return ""
}

// readMcodeCompaction captures a MiniMax Code session for a PreCompact hook.
// The hook payload's transcript_path is a temporary projection the runtime
// deletes after the call, so the session is found in the store by its id.
func readMcodeCompaction(id string) (CompactionTranscript, error) {
	path := McodeSessionPath(id)
	if path == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: no mcode session %q", ErrTranscriptIdentity, id)
	}
	return ReadCompactionSession("mcode", id, path, "")
}
