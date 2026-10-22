package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GrokHome is where Grok Build itself keeps state; GROK_HOME relocates the
// whole tree (config.toml, sessions). Install and doctor use it.
func GrokHome() string {
	return EnvPath("GROK_HOME", filepath.Join(Home(), ".grok"))
}

// GrokRoot is the session-reading root; DEJA_GROK_ROOT overrides it without
// affecting where install writes.
func GrokRoot() string {
	return EnvPath("DEJA_GROK_ROOT", GrokHome())
}

// Grok Build stores sessions by URL-encoded working directory. summary.json
// carries metadata and updates.jsonl is the authoritative ACP conversation
// stream. Grok can truncate and regrow the stream after a rewind, which looks
// like growth from the outside — the index compares the recorded prefix hash
// before resuming a parse, and reparses in full when it moved.

func GrokSessionFiles() []string {
	return walkFiles(filepath.Join(GrokRoot(), "sessions"), func(p string) bool {
		return filepath.Base(p) == "updates.jsonl"
	})
}

// GrokSidecarFiles lists what a Grok store keeps beside its transcripts: the
// summary the reader opens itself for the metadata, and the bookkeeping the
// CLI writes per session. doctor counted all of it as transcripts it could not
// read — 94 of them on a store with 11 sessions (#3319). Grok Build 1.0.41
// added usage.json and tool_definitions.json to every session (#4586).
func GrokSidecarFiles() []string {
	return walkFiles(filepath.Join(GrokRoot(), "sessions"), func(p string) bool {
		switch filepath.Base(p) {
		case "summary.json", "chat_history.jsonl", "events.jsonl", "rewind_points.jsonl",
			"prompt_context.json", "announcement_state.json", "signals.json",
			"resources_state.json", "prompt_history.jsonl", "usage.json", "tool_definitions.json":
			return true
		}
		return false
	})
}

func LoadGrok() []model.Session {
	return parseFiles(GrokSessionFiles(), ParseGrokFile)
}

type grokSummary struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary string `json:"session_summary"`
	GeneratedTitle string `json:"generated_title"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	// Grok Build records the spawn edge itself: a forked child names the
	// session it came from, a plain subagent says only what it is and its
	// parent's transcript records spawning it (grokLineage). deja reads what
	// is written and invents nothing (#1385).
	SessionKind     string `json:"session_kind"`
	ParentSessionID string `json:"parent_session_id"`
	ForkedAt        string `json:"forked_at"`
	AgentName       string `json:"agent_name"`
}

func ParseGrokFile(path string) ([]model.Session, error) {
	return parseGrokFileFromOffset(path, 0)
}

// ParseGrokFileFromOffset reads only the bytes appended past offset. A live
// session's updates.jsonl grows all day and the index used to reparse and
// rewrite the whole thing on every touch (#1522); the ingest layer only calls
// this once the recorded prefix hash still matches, so a rewind that truncates
// and regrows still goes the long way.
func ParseGrokFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseGrokFileFromOffset(path, offset)
}

func parseGrokFileFromOffset(path string, offset int64) ([]model.Session, error) {
	doc := readGrokSummary(path)
	id := doc.Info.ID
	if id == "" {
		id = filepath.Base(filepath.Dir(path))
	}
	cwd := doc.Info.CWD
	if cwd == "" {
		cwd = grokCWDFromPath(path)
	}
	title := doc.GeneratedTitle
	if title == "" {
		title = doc.SessionSummary
	}
	kind, parent := grokLineage(path, id, doc)
	s := model.Session{ID: id, Harness: "grok", Project: projectName(cwd), Path: path, Title: title,
		Kind: kind, Parent: parent, Agent: doc.AgentName}
	if t, err := time.Parse(time.RFC3339Nano, doc.CreatedAt); err == nil {
		s.Touch(t)
	}
	if t, err := time.Parse(time.RFC3339Nano, doc.UpdatedAt); err == nil {
		s.Touch(t)
	}

	// A streamed reply arrives in chunks that share a key and get joined. Tool
	// records now land between those chunks, so the join remembers where the
	// speech was rather than assuming it was the message just added.
	lastKey, lastSpeech := "", -1
	err := scanGrokUpdatesFrom(path, offset, func(event grokUpdateEvent) {
		if grokCopiedEvent(event, id, doc.ParentSessionID) {
			return
		}
		role := ""
		switch event.Params.Update.Kind {
		case "user_message_chunk":
			role = "user"
		case "agent_message_chunk":
			role = "assistant"
		case "tool_call", "tool_call_update":
			// What the agent did, which deja indexes for every other harness
			// that records it: `--role tool`, friction and the fix pairs all
			// read this. Grok kept the whole run in the transcript and the
			// parser took only the talk, so a Grok user searching `--role tool`
			// got nothing and `show` returned a conversation that looked
			// complete (#1321). Same shape as the pi parser before #1240.
			role = RoleToolOutput
		default:
			return
		}
		text := grokContentText(event.Params.Update.Content)
		if text == "" && role == RoleToolOutput {
			text = grokToolText(event.Params.Update.Content)
		}
		if text == "" && role == RoleToolOutput {
			// A call with no output yet still says what ran.
			text = grokToolTitle(event)
		}
		if text == "" {
			return
		}
		t := parseTimeAny(event.Timestamp)
		if t.IsZero() {
			t = parseTimeAny(event.Params.Meta.AgentTimestamp)
		}
		s.Touch(t)

		if role == RoleToolOutput {
			s.Messages = append(s.Messages, grokWorkRecords(event, t)...)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			return
		}
		key := grokMessageKey(role, event)
		if key != "" && key == lastKey && lastSpeech >= 0 {
			s.Messages[lastSpeech].Text += text
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		lastKey, lastSpeech = key, len(s.Messages)-1
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

type grokUpdateEvent struct {
	Timestamp json.Number `json:"timestamp"`
	Params    struct {
		Update struct {
			Kind     string          `json:"sessionUpdate"`
			Content  json.RawMessage `json:"content"`
			Title    string          `json:"title"`
			ToolKind string          `json:"kind"`
			// RawInput is the call's input, the ACP field Grok's own guide
			// names: the command for run_terminal_command, the file for
			// read_file and the edit tools (#3285).
			RawInput json.RawMessage `json:"rawInput"`
			Meta     struct {
				PromptIndex *int `json:"promptIndex"`
				Tool        struct {
					Name string `json:"name"`
				} `json:"x.ai/tool"`
			} `json:"_meta"`
		} `json:"update"`
		Meta struct {
			PromptID       string      `json:"promptId"`
			AgentTimestamp json.Number `json:"agentTimestampMs"`
			// EventID is "<session id>-<n>" of the session that wrote the
			// record; a fork's copies keep their source's.
			EventID string `json:"eventId"`
		} `json:"_meta"`
	} `json:"params"`
}

// grokCopiedEvent reports a record a `--fork-session` copy carries from its
// source. Grok 1.0.41 copies the source's records into the fork's
// updates.jsonl under the fork's sessionId and outer timestamp, keeping
// the source's eventId. Read as the fork's own, the source's turns showed up
// twice in search and blame credited the fork, stamped later, with a line its
// source wrote.
func grokCopiedEvent(event grokUpdateEvent, id, parent string) bool {
	eid := event.Params.Meta.EventID
	return parent != "" && eid != "" && !strings.HasPrefix(eid, id+"-")
}

// grokWorkRecords reads what a tool_call was asked to do off its rawInput:
// the command for run_terminal_command, the file for the tools that take one,
// and both sides of an edit.
// The reader indexed the title and the output and dropped the input, so no
// Grok session ever yielded a command or a file record (#3285).
func grokWorkRecords(event grokUpdateEvent, t time.Time) []model.Message {
	if event.Params.Update.Kind != "tool_call" || len(event.Params.Update.RawInput) == 0 {
		return nil
	}
	var in map[string]any
	if json.Unmarshal(event.Params.Update.RawInput, &in) != nil {
		return nil
	}
	name := event.Params.Update.Meta.Tool.Name
	if name == "" {
		name = strings.TrimSpace(event.Params.Update.Title)
	}
	var out []model.Message
	if name == "run_terminal_command" && IndexCommands() {
		if cmd, _ := in["command"].(string); strings.TrimSpace(cmd) != "" && worthIndexing(cmd) {
			out = append(out, model.Message{Role: RoleCommand, Text: "$ " + strings.TrimSpace(cmd), Time: t})
		}
	}
	call := []any{map[string]any{"type": "tool_use", "name": name, "input": in}}
	if IndexToolPaths() {
		if p := toolPathsIn(call, grokDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(call, grokDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(call, grokDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	return out
}

// grokDialect is Grok Build 1.0.41's default file toolset: read_file takes
// `target_file`, search_replace {file_path, old_string, new_string} and write
// {file_path, content}. Reading only target_file left every change a Grok
// session made as a bare title (#4497).
var grokDialect = toolDialect{
	pathKey:    "file_path",
	pathKeyAlt: "target_file",
	pathTools:  map[string]bool{"read_file": true, "search_replace": true, "write": true},
	editTools:  map[string]bool{"search_replace": true, "write": true},
}

// GrokResumes reports whether the tail of updates.jsonl can be appended to
// what is stored. Chunks that share a key are joined into one message, across
// the tool records between them; when the tail's first chunk continues the
// message the stored part ended on, the file is read whole (#4445).
func GrokResumes(path string, offset int64) bool {
	return resumesUnlessJoined(path, offset, func(line []byte) (string, bool) {
		role := ""
		switch {
		case bytes.Contains(line, grokUserChunk):
			role = "user"
		case bytes.Contains(line, grokAgentChunk):
			role = "assistant"
		default:
			return "", false
		}
		var event grokUpdateEvent
		if json.Unmarshal(line, &event) != nil || grokContentText(event.Params.Update.Content) == "" {
			return "", false
		}
		return grokMessageKey(role, event), true
	})
}

func grokMessageKey(role string, event grokUpdateEvent) string {
	if role == "assistant" {
		if event.Params.Meta.PromptID != "" {
			return role + ":" + event.Params.Meta.PromptID
		}
		return ""
	}
	if event.Params.Update.Meta.PromptIndex != nil {
		return role + ":" + strconv.Itoa(*event.Params.Update.Meta.PromptIndex)
	}
	return ""
}

// grokToolTitle names the call when it carried no output: "bash" reads as work
// done, an empty record reads as nothing happening.
func grokToolTitle(event grokUpdateEvent) string {
	title := strings.TrimSpace(event.Params.Update.Title)
	kind := strings.TrimSpace(event.Params.Update.ToolKind)
	switch {
	case title != "" && kind != "":
		return kind + ": " + title
	case title != "":
		return title
	default:
		return kind
	}
}

// grokToolText reads the shape ACP uses for a tool call's output, which wraps
// each block one level deeper than a spoken message:
//
//	[{"type":"content","content":{"type":"text","text":"..."}}]
//
// Kept next to the Grok parser rather than folded into the shared extractors:
// those are read by every other harness, and this nesting is this protocol's.
func grokToolText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Text    string `json:"text"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		txt := block.Text
		if txt == "" && (block.Content.Type == "" || block.Content.Type == "text") {
			txt = block.Content.Text
		}
		if txt == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(txt)
	}
	return b.String()
}

func grokContentText(raw json.RawMessage) string {
	var block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &block) == nil && block.Text != "" {
		if block.Type == "" || block.Type == "text" {
			return block.Text
		}
		return ""
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&v)
	return textFromContent(v)
}

var grokUserChunk = []byte(`"user_message_chunk"`)
var grokAgentChunk = []byte(`"agent_message_chunk"`)
var grokToolCall = []byte(`"sessionUpdate":"tool_call"`)
var grokToolUpdate = []byte(`"sessionUpdate":"tool_call_update"`)

// Most update lines are large tool events. Filter them before JSON decoding so
// indexing cost is dominated by reading the log rather than materializing tool
// payloads that will be discarded.
func scanGrokUpdates(path string, fn func(grokUpdateEvent)) error {
	return scanGrokUpdatesFrom(path, 0, fn)
}

func scanGrokUpdatesFrom(path string, offset int64, fn func(grokUpdateEvent)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := jsonlReaders.Get().(*bufio.Reader)
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(boundedFrom(path, f, offset))
	for {
		line, partial, err := readLineBytes(r)
		if partial {
			return nil
		}
		if bytes.Contains(line, grokUserChunk) || bytes.Contains(line, grokAgentChunk) || bytes.Contains(line, grokToolCall) || bytes.Contains(line, grokToolUpdate) {
			var event grokUpdateEvent
			if json.Unmarshal(trimJSONSpace(line), &event) == nil {
				fn(event)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// grokLineage is the kind and parent recall needs to keep a session's own
// relatives out of its answers.
//
// "headless" is how a `grok -p` run is started, not what spawned it: a
// top-level session from a shell. Kept as a kind, every scripted session read
// as a subagent whose parent was lost (#4585).
//
// A `--fork-session` copy names its source and when it forked, under whatever
// kind the run had; it reads as a fork, or the source came back to the fork as
// prior history. A subagent's summary names no parent at all on 1.0.41: the
// edge is the parent's own subagent_spawned record, which names both ends, so
// the parent is read off that record among the sessions of the same directory.
func grokLineage(path, id string, doc grokSummary) (kind, parent string) {
	kind, parent = doc.SessionKind, doc.ParentSessionID
	if kind == "headless" {
		kind = ""
	}
	spawned := kind == "subagent" || kind == "subagent_fork"
	if parent != "" && doc.ForkedAt != "" && !spawned {
		kind = "fork"
	}
	if parent == "" && spawned {
		parent = grokSpawner(path, id, doc.CreatedAt)
	}
	return kind, parent
}

// grokSpawners keeps what grokSpawner found for this process: a live subagent
// is parsed again on every append, and its parent does not change.
var grokSpawners sync.Map

// grokSpawner is the parent a subagent_spawned record in the same directory
// names for child. Only a session created before the child and written to
// since can hold that record, so the rest are not opened. The record is read
// for its parent_session_id rather than taken from the file it sits in: a
// fork of the parent copies the parent's records.
func grokSpawner(path, child, created string) string {
	if child == "" {
		return ""
	}
	if p, ok := grokSpawners.Load(child); ok {
		return p.(string)
	}
	own := filepath.Dir(path)
	group := filepath.Dir(own)
	entries, err := os.ReadDir(group)
	if err != nil {
		return ""
	}
	since, _ := time.Parse(time.RFC3339Nano, created)
	// Encoded, not spliced: an id with a quote or backslash in it is written
	// escaped in the record.
	quoted, err := json.Marshal(child)
	if err != nil {
		return ""
	}
	needle := append([]byte(`"child_session_id":`), quoted...)
	for _, e := range entries {
		dir := filepath.Join(group, e.Name())
		if !e.IsDir() || dir == own {
			continue
		}
		updates := filepath.Join(dir, "updates.jsonl")
		if !since.IsZero() {
			fi, err := os.Stat(updates)
			if err != nil || fi.ModTime().Before(since.Add(-time.Minute)) {
				continue
			}
			if c, err := time.Parse(time.RFC3339Nano, readGrokSummary(updates).CreatedAt); err == nil && c.After(since) {
				continue
			}
		}
		line := fileLineWith(updates, needle)
		if line == nil {
			continue
		}
		var rec struct {
			Params struct {
				Update struct {
					Kind   string `json:"sessionUpdate"`
					Parent string `json:"parent_session_id"`
				} `json:"update"`
			} `json:"params"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Params.Update.Kind == "subagent_spawned" && rec.Params.Update.Parent != "" {
			grokSpawners.Store(child, rec.Params.Update.Parent)
			return rec.Params.Update.Parent
		}
	}
	return ""
}

// fileLineWith is the first subagent_spawned record of the file holding
// needle, or nil.
func fileLineWith(path string, needle []byte) []byte {
	if !fileContains(path, needle) {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	r := jsonlReaders.Get().(*bufio.Reader)
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(f)
	for {
		line, partial, err := readLineBytes(r)
		if partial {
			return nil
		}
		if bytes.Contains(line, needle) && bytes.Contains(line, []byte(`"subagent_spawned"`)) {
			return bytes.Clone(line)
		}
		if err != nil {
			return nil
		}
	}
}

// fileContains reports whether needle occurs in the file, read in chunks that
// overlap by the needle's length so a match across a boundary is not lost.
func fileContains(path string, needle []byte) bool {
	f, err := os.Open(path)
	if err != nil || len(needle) == 0 {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 0, 256<<10)
	chunk := make([]byte, 256<<10)
	for {
		n, err := f.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if bytes.Contains(buf, needle) {
			return true
		}
		if keep := len(needle) - 1; len(buf) > keep {
			buf = append(buf[:0], buf[len(buf)-keep:]...)
		}
		if err != nil {
			return false
		}
	}
}

// IsGrokTranscript reports whether path is a Grok Build session's
// updates.jsonl, the transcript its hooks name.
func IsGrokTranscript(path string) bool {
	if filepath.Base(path) != "updates.jsonl" {
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), "summary.json"))
	return err == nil
}

// GrokSessionHead is a Grok Build session's identity off its summary.json:
// enough for recall to leave out its source or parent before any index has
// read it. The transcript is the hook's when it names one; a hook or MCP
// call that names only the id finds it under the sessions tree.
func GrokSessionHead(id, transcript string) (model.Session, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Session{}, false
	}
	path := ""
	if transcript != "" && IsGrokTranscript(transcript) {
		path = transcript
	} else if !strings.ContainsAny(id, `/\`) && id != "." && id != ".." {
		sessions := filepath.Join(GrokRoot(), "sessions")
		groups, _ := os.ReadDir(sessions)
		for _, g := range groups {
			p := filepath.Join(sessions, g.Name(), id, "updates.jsonl")
			if g.IsDir() && IsGrokTranscript(p) {
				path = p
				break
			}
		}
	}
	if path == "" {
		return model.Session{}, false
	}
	doc := readGrokSummary(path)
	if doc.Info.ID != "" && doc.Info.ID != id {
		return model.Session{}, false
	}
	kind, parent := grokLineage(path, id, doc)
	return model.Session{ID: id, Harness: "grok", Path: path, Kind: kind, Parent: parent}, true
}

// GrokChatHolds reports whether the session's chat_history.jsonl, what Grok
// sends the model and replays on resume, holds text as a message carried it.
// The text is matched JSON-encoded, so a tool's output that quotes it, which
// is encoded once more, does not count. A fork's copy holds what its source
// was told.
func GrokChatHolds(transcript, text string) bool {
	if !IsGrokTranscript(transcript) {
		return false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(text) != nil {
		return false
	}
	needle := bytes.TrimSuffix(bytes.TrimSpace(buf.Bytes()), []byte(`"`))
	needle = bytes.TrimPrefix(needle, []byte(`"`))
	return fileContains(filepath.Join(filepath.Dir(transcript), "chat_history.jsonl"), needle)
}

func readGrokSummary(updatePath string) grokSummary {
	var doc grokSummary
	b, err := readJSONFile(filepath.Join(filepath.Dir(updatePath), "summary.json"))
	if err == nil {
		_ = json.Unmarshal(b, &doc)
	}
	return doc
}

// GrokCWDForSession recovers the working directory needed by grok --resume.
func GrokCWDForSession(updatePath string) string {
	if updatePath == "" {
		return ""
	}
	if cwd := readGrokSummary(updatePath).Info.CWD; cwd != "" {
		return cwd
	}
	return grokCWDFromPath(updatePath)
}

func grokCWDFromPath(updatePath string) string {
	if updatePath == "" {
		return ""
	}
	group := filepath.Dir(filepath.Dir(updatePath))
	if b, err := os.ReadFile(filepath.Join(group, ".cwd")); err == nil {
		if cwd := strings.TrimSpace(string(b)); cwd != "" {
			return cwd
		}
	}
	cwd, err := url.PathUnescape(filepath.Base(group))
	if err != nil {
		return ""
	}
	return cwd
}
