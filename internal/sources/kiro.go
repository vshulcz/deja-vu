package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kiro (kiro.dev, Amazon's agent) keeps two file stores under ~/.kiro/sessions,
// written by its two clients, and they are different formats.
//
// The CLI writes a pair per session:
//
//	cli/<sessionId>.json   header — session_id, cwd, the model and turn metadata
//	cli/<sessionId>.jsonl  the conversation, one record per line
//
// A record is `{"kind":"Prompt"|"AssistantMessage","data":{…}}` with the text in
// `data.content[].data` under `kind:"text"`, and `data.meta.timestamp` in
// seconds. Several `AssistantMessage` records can share one `data.message_id`:
// the client appends the reply as it streams, and each record carries the next
// piece rather than the whole answer so far. Read one per record and a recall
// quotes a third of a sentence, so a run under one id is joined in order
// (tokscale's reader sums the same records for the same reason —
// crates/tokscale-core/src/sessions/kiro.rs).
//
// The IDE — Kiro is a VS Code fork — writes per workspace instead:
//
//	<workspace>/sess_<uuid>/session.json    id, modelId, workspacePaths, createdAt
//	<workspace>/sess_<uuid>/messages.jsonl  the conversation
//
// where a line is `{"timestamp":<RFC3339>,"payload":{"type":"user"|"assistant",
// "content":"…"}}`. Older builds wrote the flat `{"role":…,"content":…}` shape
// into the same file, so both are read.
//
// `kiro-cli chat --no-interactive` keeps its conversations in a SQLite store
// instead (`kiro-cli/data.sqlite3`, table `conversations_v2`); kiro_db.go
// reads it (#4300).
//
// What is not read yet, and why: the IDE also mirrors chats into its
// globalStorage (`kiro.kiroagent/<workspace>/*.chat` beside extensionless
// execution records). No sample here covers that shape, and a reader written
// against a guess is a reader that silently drops history (#3103).

// KiroConfigDir is Kiro's user directory: sessions, settings, agents.
func KiroConfigDir() string { return filepath.Join(Home(), ".kiro") }

// KiroRoot is the session store root. DEJA_KIRO_ROOT replaces it.
func KiroRoot() string {
	return EnvPath("DEJA_KIRO_ROOT", filepath.Join(KiroConfigDir(), "sessions"))
}

// KiroCLIFiles lists the CLI transcripts: the .jsonl half of each pair.
func KiroCLIFiles() []string {
	return walkFiles(filepath.Join(KiroRoot(), "cli"), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl")
	})
}

// KiroIDEFiles lists the IDE transcripts, which are named rather than numbered.
func KiroIDEFiles() []string {
	return walkFiles(KiroRoot(), func(p string) bool {
		return filepath.Base(p) == "messages.jsonl" && kiroUnderSessDir(p)
	})
}

// KiroSessionFiles is everything a Kiro install has on disk: both transcript
// layouts, and kiro-cli's database when it holds anything.
func KiroSessionFiles() []string {
	out := append(KiroCLIFiles(), KiroIDEFiles()...)
	if fi, err := os.Stat(KiroDB()); err == nil && fi.Size() > 0 {
		out = append(out, KiroDB())
	}
	return out
}

// KiroUnderCLI reports whether a path is a CLI transcript of this store, so the
// registry can claim it for incremental ingest.
func KiroUnderCLI(p string) bool {
	return strings.HasPrefix(p, filepath.Join(KiroRoot(), "cli")) && strings.HasSuffix(p, ".jsonl")
}

// KiroUnderIDE is the same question for the IDE layout.
func KiroUnderIDE(p string) bool {
	return filepath.Base(p) == "messages.jsonl" &&
		strings.HasPrefix(p, KiroRoot()) && kiroUnderSessDir(p)
}

// kiroUnderSessDir reports whether a transcript sits in a `sess_<uuid>`
// directory, which is what separates an IDE session from anything else under
// the root.
func kiroUnderSessDir(p string) bool {
	return strings.HasPrefix(filepath.Base(filepath.Dir(p)), "sess_")
}

func LoadKiro() []model.Session {
	ss := parseFiles(KiroCLIFiles(), ParseKiroCLIFile)
	ss = append(ss, parseFiles(KiroIDEFiles(), ParseKiroIDEFile)...)
	dbSS, _ := ParseKiroDB(KiroDB())
	return append(ss, dbSS...)
}

// ParseKiroCLIFile reads one CLI transcript.
func ParseKiroCLIFile(path string) ([]model.Session, error) {
	return ParseKiroCLIFileFromOffset(path, 0)
}

// ParseKiroCLIFileFromOffset is the incremental read. The header is a separate
// file, so it is read on every pass rather than depending on the watermark.
func ParseKiroCLIFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "kiro",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	applyKiroCLIHeader(&s, strings.TrimSuffix(path, ".jsonl")+".json")

	// A streamed reply arrives as several records under one message id. Joining
	// them needs the run to be contiguous, which it is: the client appends as
	// the answer comes in. lastAt is the reply a run joins, which is not the
	// last message once a record's tool calls follow it.
	var lastID string
	lastAt := -1
	// kiro-cli 2.22.0 stamps only the Prompt; a reply and its tool calls take
	// the prompt's time, or a file the agent wrote sits at no time at all and
	// `deja files` cannot place it near anything that was said. On a resumed
	// read that Prompt is behind the offset, and what was appended after a
	// pass landed at 0001-01-01 (#4444).
	at := kiroCLITimeBefore(path, offset)
	exits := commandExits{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		kind, _ := m["kind"].(string)
		data, _ := m["data"].(map[string]any)
		if data == nil {
			return
		}
		text, calls, results := kiroContent(data["content"])
		t := parseTimeAny(kiroMetaTimestamp(data))
		if t.IsZero() {
			t = at
		}
		at = t
		id, _ := data["message_id"].(string)
		switch {
		case text == "":
		case kind == "Prompt":
			lastID, lastAt = "", -1
			s.Touch(t)
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
		case kind == "AssistantMessage":
			s.Touch(t)
			if id != "" && id == lastID && lastAt >= 0 {
				s.Messages[lastAt].Text += text
				break
			}
			lastID, lastAt = id, len(s.Messages)
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: t})
		}
		// The work rides beside the talk: toolUse parts in an AssistantMessage,
		// toolResult parts in a ToolResults record. Read as text only, a Kiro
		// CLI session had no command, no file and no tool output (#4299).
		from := len(s.Messages)
		if work := kiroWorkRecords(calls, results, t); len(work) > 0 {
			s.Touch(t)
			s.Messages = append(s.Messages, work...)
		}
		exits.note(s.Messages, from, commandCallsIn(calls, kiroDialect))
		parts, _ := data["content"].([]any)
		for _, part := range parts {
			if p, _ := part.(map[string]any); p["kind"] == "toolResult" {
				d, _ := p["data"].(map[string]any)
				if code, ok := kiroExitStatus(d["content"]); ok {
					exits.stamp(s.Messages, str(d["toolUseId"]), "", code)
				}
			}
		}
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kiroCLITimeBefore is the time of the last stamped record before offset,
// the one a record after it takes when it carries none.
func kiroCLITimeBefore(path string, offset int64) time.Time {
	var at time.Time
	if offset <= 0 {
		return at
	}
	eachLineBefore(path, offset, func(line []byte) bool {
		if data, _ := decodeJSONLine(line)["data"].(map[string]any); data != nil {
			at = parseTimeAny(kiroMetaTimestamp(data))
		}
		return at.IsZero()
	})
	return at
}

// KiroCLIResumes reports whether a CLI transcript's tail can be appended to
// what is stored. A reply streams in as AssistantMessage records under one
// message_id, joined as they are read; when the tail continues the reply the
// stored part ended on, it is read whole (#4445). So is a tail holding the
// failed exit of a command called before it, which only a read holding both
// can stamp (#4505).
func KiroCLIResumes(path string, offset int64) bool {
	return kiroExitResumes(path, offset) && resumesUnlessJoined(path, offset, func(line []byte) (string, bool) {
		m := decodeJSONLine(line)
		data, _ := m["data"].(map[string]any)
		if data == nil {
			return "", false
		}
		if text, _, _ := kiroContent(data["content"]); text == "" {
			return "", false
		}
		switch m["kind"] {
		case "Prompt":
			return "", true
		case "AssistantMessage":
			id, _ := data["message_id"].(string)
			return id, true
		}
		return "", false
	})
}

// kiroExitResumes is the #4443 rule for kiro-cli: a clean result left in the
// next pass is let go, as it is for pi, and a failed one is not.
var kiroExitResumes = resumesUnlessAnswering(`"toolUseId"`, func(m map[string]any) ([]string, string) {
	data, _ := m["data"].(map[string]any)
	parts, _ := data["content"].([]any)
	var calls []string
	for _, part := range parts {
		p, _ := part.(map[string]any)
		d, _ := p["data"].(map[string]any)
		id := str(d["toolUseId"])
		switch p["kind"] {
		case "toolUse":
			calls = append(calls, id)
		case "toolResult":
			if code, ok := kiroExitStatus(d["content"]); ok && code != 0 {
				return calls, id
			}
		}
	}
	return calls, ""
})

// applyKiroCLIHeader reads identity out of the header file beside the
// transcript: the session's own id and the directory it ran in.
func applyKiroCLIHeader(s *model.Session, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var header struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}
	if json.Unmarshal(b, &header) != nil {
		return
	}
	if header.SessionID != "" {
		s.ID = header.SessionID
	}
	if header.CWD != "" {
		s.Project = projectName(header.CWD)
	}
}

// KiroSessionDir is the directory a Kiro session ran in, from the metadata
// beside its transcript: the CLI header's cwd, or the first of a sess_
// session's workspacePaths. "" when neither names one.
func KiroSessionDir(path string) string {
	if kiroUnderSessDir(path) {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "session.json"))
		if err != nil {
			return ""
		}
		var header struct {
			WorkspacePaths []string `json:"workspacePaths"`
		}
		if json.Unmarshal(b, &header) != nil || len(header.WorkspacePaths) == 0 {
			return ""
		}
		return header.WorkspacePaths[0]
	}
	b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".json")
	if err != nil {
		return ""
	}
	var header struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(b, &header) != nil {
		return ""
	}
	return header.CWD
}

// KiroV3Session reports whether a sess_ session is one `kiro-cli --v3` lists.
// V3 writes the same <workspace>/sess_<uuid> layout as the IDE, and also adds
// the session to session-index/<workspace>.jsonl beside the sessions root;
// that entry is what tells the two apart (#4307).
func KiroV3Session(path string) bool {
	if !kiroUnderSessDir(path) {
		return false
	}
	sess := filepath.Dir(path)
	ws := filepath.Dir(sess)
	index := filepath.Join(filepath.Dir(filepath.Dir(ws)), "session-index", filepath.Base(ws)+".jsonl")
	want := filepath.Base(ws) + "/" + filepath.Base(sess)
	listed := false
	_ = scanJSONLFromOffset(index, 0, func(m map[string]any) {
		if p, _ := m["sessionPath"].(string); filepath.ToSlash(p) == want {
			op, _ := m["op"].(string)
			listed = op != "remove" && op != "delete"
		}
	})
	return listed
}

// kiroContent splits a CLI record's content array, whose parts name their own
// kind, into the text, the tool calls (as tool_use parts the shared extractors
// read) and the text of the tool results.
func kiroContent(v any) (string, []any, []string) {
	parts, ok := v.([]any)
	if !ok {
		return textFromContent(v), nil, nil
	}
	var b strings.Builder
	var calls []any
	var results []string
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		switch kind, _ := p["kind"].(string); kind {
		case "", "text":
			if s, _ := p["data"].(string); s != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(s)
			}
		case "toolUse":
			d, _ := p["data"].(map[string]any)
			name, _ := d["name"].(string)
			if in, ok := d["input"].(map[string]any); ok && name != "" {
				call := kiroToolCall(name, in)
				call["id"] = d["toolUseId"]
				calls = append(calls, call)
			}
		case "toolResult":
			d, _ := p["data"].(map[string]any)
			if out := kiroResultText(d["content"]); out != "" {
				results = append(results, out)
			}
		}
	}
	return b.String(), calls, results
}

// kiroDialect is kiro-cli's tool vocabulary. The TUI calls its tools shell,
// write and read; `--no-interactive` keeps the older execute_bash, fs_write
// and fs_read. The two write tools name their arguments differently and
// kiroToolCall folds them onto the keys here.
var kiroDialect = toolDialect{
	pathKey:     "path",
	pathTools:   map[string]bool{"write": true, "read": true, "fs_write": true, "fs_read": true},
	shellTools:  map[string]bool{"shell": true, "execute_bash": true, "execute_cmd": true},
	editTools:   map[string]bool{"write": true, "fs_write": true},
	oldKey:      "old_str",
	newKey:      "new_str",
	pathListKey: "operations",
}

// kiroToolCall is one call in the tool_use shape. `write` takes
// content/oldStr/newStr where fs_write takes file_text/old_str/new_str, and a
// read lists its targets under `operations`, where a Directory one is a
// listing rather than a file the session touched.
func kiroToolCall(name string, args map[string]any) map[string]any {
	in := make(map[string]any, len(args))
	for k, v := range args {
		in[k] = v
	}
	for from, to := range map[string]string{"oldStr": "old_str", "newStr": "new_str", "file_text": "content"} {
		if v, ok := in[from]; ok {
			if _, set := in[to]; !set {
				in[to] = v
			}
		}
	}
	if ops, ok := in["operations"].([]any); ok {
		var files []any
		for _, op := range ops {
			if m, ok := op.(map[string]any); ok && m["mode"] != "Directory" {
				files = append(files, op)
			}
		}
		in["operations"] = files
	}
	return map[string]any{"type": "tool_use", "name": name, "input": in}
}

// kiroResultText reads a tool result's content. The TUI writes its parts as
// {"kind":"text"|"json","data":…}, the database as {"Text":…} or {"Json":…};
// a shell result's JSON is its exit status and output, and the output is the
// part worth reading.
func kiroResultText(v any) string {
	parts, _ := v.([]any)
	var out []string
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		data, ok := p["data"]
		if !ok {
			if data, ok = p["Text"]; !ok {
				data = p["Json"]
			}
		}
		switch d := data.(type) {
		case string:
			if strings.TrimSpace(d) != "" {
				out = append(out, d)
			}
		case map[string]any:
			_, hasOut := d["stdout"]
			_, hasErr := d["stderr"]
			if !hasOut && !hasErr {
				if b, err := json.Marshal(d); err == nil {
					out = append(out, string(b))
				}
				continue
			}
			for _, k := range []string{"stdout", "stderr"} {
				if s, _ := d[k].(string); strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// kiroExitStatus is how a shell command ended, from its result's JSON part:
// "exit status: 1" in the TUI transcript, "1" in the database. kiroResultText
// keeps only the output, and a failed command was stored without its status
// (#4505).
func kiroExitStatus(v any) (int, bool) {
	parts, _ := v.([]any)
	for _, part := range parts {
		p, _ := part.(map[string]any)
		data, ok := p["data"].(map[string]any)
		if !ok {
			data, _ = p["Json"].(map[string]any)
		}
		if st, ok := data["exit_status"].(string); ok {
			return statusCode(strings.TrimPrefix(st, "exit status: "), "", "")
		}
	}
	return 0, false
}

// kiroWorkRecords turns one record's tool calls and results into work records,
// the way the other readers do for their own dialects.
func kiroWorkRecords(calls []any, results []string, t time.Time) []model.Message {
	var out []model.Message
	if len(calls) > 0 {
		if IndexToolPaths() {
			if p := toolPathsIn(calls, kiroDialect); p != "" {
				out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
			}
		}
		if IndexWrites() {
			for _, w := range wroteRecordsIn(calls, kiroDialect) {
				out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
			}
		}
		if IndexEdits() {
			for _, span := range editSpansIn(calls, kiroDialect) {
				out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
			}
		}
		if IndexCommands() {
			for _, cmd := range commandsIn(calls, kiroDialect) {
				out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: t})
			}
		}
	}
	if IndexToolOutput() {
		for _, r := range results {
			out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(r), Time: t})
		}
	}
	return out
}

func kiroMetaTimestamp(data map[string]any) any {
	meta, _ := data["meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	return meta["timestamp"]
}

// ParseKiroIDEFile reads one IDE transcript.
func ParseKiroIDEFile(path string) ([]model.Session, error) {
	return ParseKiroIDEFileFromOffset(path, 0)
}

// ParseKiroIDEFileFromOffset is the incremental read.
func ParseKiroIDEFileFromOffset(path string, offset int64) ([]model.Session, error) {
	dir := filepath.Dir(path)
	s := model.Session{
		Harness: "kiro",
		ID:      filepath.Base(dir),
		Project: claudeProjectName(pathToProjectKey(filepath.Base(filepath.Dir(dir)))),
		Path:    path,
	}
	applyKiroIDEHeader(&s, filepath.Join(dir, "session.json"))

	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		t := parseTimeAny(m["timestamp"])
		role, text := kiroIDELine(m)
		if text == "" {
			return
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kiroIDELine reads one IDE record, in either of the two shapes that file has
// held: the payload wrapper of current builds and the flat role/content line of
// the ones before it.
func kiroIDELine(m map[string]any) (string, string) {
	payload, _ := m["payload"].(map[string]any)
	if payload == nil {
		payload = m
	}
	kind, _ := payload["type"].(string)
	if kind == "" {
		kind, _ = payload["role"].(string)
	}
	text := textFromContent(payload["content"])
	switch kind {
	case "user", "human", "prompt":
		return "user", text
	case "assistant", "bot", "response":
		return "assistant", text
	case "tool_result":
		return RoleToolOutput, text
	}
	return "", ""
}

// applyKiroIDEHeader reads the metadata half of an IDE session: its own id and
// the workspace it ran in, which is more precise than the directory name the
// path carries (that one is folded).
func applyKiroIDEHeader(s *model.Session, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var header struct {
		ID             string   `json:"id"`
		WorkspacePaths []string `json:"workspacePaths"`
		CreatedAt      string   `json:"createdAt"`
	}
	if json.Unmarshal(b, &header) != nil {
		return
	}
	if header.ID != "" {
		s.ID = header.ID
	}
	if len(header.WorkspacePaths) > 0 && header.WorkspacePaths[0] != "" {
		s.Project = projectName(header.WorkspacePaths[0])
	}
	s.Touch(parseTimeAny(header.CreatedAt))
}
