package sources

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
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
// skill catalogue, the media reminder. canonicalTextRange says which part of
// that text was typed, and indexing the rest would put the harness's own words
// at the head of every session on the machine and in every answer about them.
//
// The thinking block is the model reasoning out loud, not what it told the
// person, so it stays out of the transcript the way it does in every other
// harness here.
//
// The transcript holds no working directory, so the project comes from where
// the session's own file activity was: the same evidence the other harnesses
// without a recorded cwd are filed under (#542).
//
// Read off MiniMax Code's local runtime v2 sessions, 2026-10-20.

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
func McodeSessionFiles() []string {
	return walkFiles(McodeRoot(), func(p string) bool {
		return filepath.Base(p) == mcodeTranscriptName &&
			strings.HasPrefix(p, McodeRoot()+string(filepath.Separator))
	})
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
		if names[filepath.Base(p)] {
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
			mcodeApplyUser(&s, m)
		case "assistant":
			mcodeApplyAssistant(&s, msg)
		case "compactionSummary":
			mcodeApplySummary(&s, msg)
		case "toolResult":
			t := parseTimeAny(msg["timestamp"])
			text := mcodeText(msg["content"])
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
	if p := projectFromPaths(s.Messages); p != "" {
		s.Project = p
	} else {
		s.ProjectFromDir = true
	}
	return []model.Session{s}, err
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

// decodeMcodeID decodes the base64 the directory name carries the id in.
func decodeMcodeID(enc string) (string, bool) {
	for _, enc := range []string{strings.TrimRight(enc, "="), enc} {
		raw, err := base64.RawStdEncoding.DecodeString(enc)
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
	role, _ := msg["role"].(string)
	return role == "user" || role == "assistant" || role == "toolResult"
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

// mcodeApplyUserTitle takes only the title from a turn, for the line a
// resumed read reads again. The turn itself is already in the index, and
// adding it a second time would show the person their own question twice.
func mcodeApplyUserTitle(s *model.Session, m map[string]any) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
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

// mcodeUserText is what the person wrote in this turn: the canonical text the
// runtime marked, or the whole turn when it marked none.
func mcodeUserText(msg map[string]any) string {
	text := mcodeText(msg["content"])
	if ctr, ok := msg["canonicalTextRange"].(map[string]any); ok {
		// The range counts into the turn's own text, which is one block: the
		// runtime wraps the request in the context it assembled and marks
		// what the person wrote inside it.
		if cut, ok := mcodeCanonicalText(mcodeFirstText(msg["content"]), ctr); ok {
			text = cut
		}
	}
	return strings.TrimSpace(text)
}

// mcodeCanonicalText cuts the text the person typed out of the text the
// runtime assembled around it. The offsets are the ones a JavaScript string
// reports — UTF-16 code units — because the runtime is one, and a Chinese
// character is one unit there and three bytes here.
func mcodeCanonicalText(text string, ctr map[string]any) (string, bool) {
	start, ok := mcodeOffset(ctr["startOffset"])
	if !ok {
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
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case float64:
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

// mcodeFirstText is the first text block of a turn, which is where the
// runtime's assembled context and the person's own words sit together.
func mcodeFirstText(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		return ""
	}
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "text" {
			continue
		}
		if text, _ := m["text"].(string); text != "" {
			return text
		}
	}
	return ""
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
// the machine holds no such session. The id is the one the runtime shows, the
// manifest's, and the directory name carries it base64-encoded.
func McodeSessionPath(id string) string {
	if id == "" {
		return ""
	}
	for _, p := range McodeSessionFiles() {
		if mcodeSessionID(p) == id {
			return p
		}
	}
	return ""
}

// McodeSessionTime is when the session with this id ran, read from the
// manifest's clock rather than from a message, so a session with no message
// yet still has one.
func McodeSessionTime(id string) (time.Time, bool) {
	path := McodeSessionPath(id)
	if path == "" {
		return time.Time{}, false
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "manifest.json"))
	if err != nil {
		return time.Time{}, false
	}
	var m struct {
		CreatedAtMs json.Number `json:"createdAtMs"`
	}
	if json.Unmarshal(b, &m) != nil {
		return time.Time{}, false
	}
	t := parseTimeAny(m.CreatedAtMs)
	return t, !t.IsZero()
}
