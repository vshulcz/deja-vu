package sources

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CodeWhale is the Rust terminal agent that shipped as deepseek-tui until
// v0.8.41 and under its own name since. It is not the DeepSeek Harness deja
// reads as "deepseek": that one keeps zstd-framed JSONL under ~/.dsh, while
// this one writes one pretty-printed JSON file per session:
//
//	~/.codewhale/sessions/<id>.json
//
// A session is {schema_version, metadata, messages}, where a message is
// {role, content} and content is a list of blocks tagged by `type` — the
// Anthropic shape the Cline and Claude readers already take apart, so the tool
// calls, the files they touched and what they printed come through the same
// helpers. $CODEWHALE_HOME moves the whole store, and a machine that has not
// finished the rebrand still keeps ~/.deepseek/sessions, which the harness
// itself reads and migrates on first access — so both roots are walked.
//
// Messages carry no timestamps of their own: the session's created_at is the
// clock, one millisecond per record, the way the Zed reader keeps two identical
// turns apart (#3333).

// CodeWhaleConfigDir is the store root: $CODEWHALE_HOME, else ~/.codewhale.
func CodeWhaleConfigDir() string {
	return EnvPath("CODEWHALE_HOME", filepath.Join(Home(), ".codewhale"))
}

// CodeWhaleLegacyConfigDir is the pre-rebrand root the harness still migrates
// from. An explicit $CODEWHALE_HOME is an isolation boundary in the harness's
// own resolver, so deja does not reach outside it either.
func CodeWhaleLegacyConfigDir() string {
	if os.Getenv("CODEWHALE_HOME") != "" {
		return ""
	}
	return filepath.Join(Home(), ".deepseek")
}

// CodeWhaleRoot is the session directory. DEJA_CODEWHALE_ROOT replaces it.
func CodeWhaleRoot() string {
	return EnvPath("DEJA_CODEWHALE_ROOT", filepath.Join(CodeWhaleConfigDir(), "sessions"))
}

// CodeWhaleRoots is every session directory to walk: the current one, and the
// legacy one while it is still there.
func CodeWhaleRoots() []string {
	out := []string{CodeWhaleRoot()}
	if legacy := CodeWhaleLegacyConfigDir(); legacy != "" {
		p := filepath.Join(legacy, "sessions")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// codeWhaleSidecars are the files the harness keeps beside its transcripts:
// the offline queue, the legacy checkpoint slot, and the ledger of which boot
// owns which session (session_boot_owners.json, #4361).
// None of them is a session, and the row that counts unread files should not
// report them as transcripts deja failed on.
var codeWhaleSidecars = map[string]bool{
	"offline_queue.json":       true,
	"latest.json":              true,
	"session_boot_owners.json": true,
	"constitution.json":        true,
}

// CodeWhaleSessionFiles lists the transcripts. A session file sits directly in
// the sessions directory and is named after its id; checkpoints, artifacts and
// goals live in subdirectories of their own.
func CodeWhaleSessionFiles() []string {
	var out []string
	for _, root := range CodeWhaleRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" || codeWhaleSidecars[e.Name()] {
				continue
			}
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// CodeWhaleSidecarFiles names the files above, so doctor can place them rather
// than count them as transcripts it could not read.
func CodeWhaleSidecarFiles() []string {
	var out []string
	for _, root := range CodeWhaleRoots() {
		for name := range codeWhaleSidecars {
			p := filepath.Join(root, name)
			if _, err := os.Stat(p); err == nil {
				out = append(out, p)
			}
		}
		// A transcript sits directly in the root, so everything below it is
		// bookkeeping: checkpoints/, and since 0.10.0 a directory per session
		// (approval receipts, runtime state), .late-usage/ and
		// .work-graph-import-archive/, which keeps copies of migrated sessions.
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, walkFiles(filepath.Join(root, e.Name()), func(string) bool { return true })...)
			}
		}
	}
	return out
}

// codeWhaleDialect is what CodeWhale calls its tools. The shell tool answers to
// three names on the wire — the canonical exec_shell and the bash aliases every
// model already knows — and the file tools take `path`. apply_patch carries a
// unified diff or whole files rather than a replaced span; codeWhalePatchRecords
// reads it (#4538). Since
// 0.9.6 new turns use read, write and edit instead, and edit takes
// edits[{oldText,newText}]; the older names stay for sessions saved before
// (#4360).
var codeWhaleDialect = toolDialect{
	pathKey: "path",
	pathTools: map[string]bool{
		"read": true, "write": true, "edit": true,
		"read_file": true, "write_file": true, "edit_file": true,
		"fim_edit": true, "str_replace": true,
	},
	shellTool: "exec_shell",
	// terminal/run runs a command in a PTY session and task_shell_start as a
	// background task, both under `command` (#4538).
	shellTools: map[string]bool{"exec_shell": true, "bash": true, "Bash": true, "terminal/run": true, "task_shell_start": true},
	editTools: map[string]bool{
		"edit": true, "write": true,
		"edit_file": true, "fim_edit": true, "str_replace": true,
	},
	oldKey:      "old_string",
	editsOldKey: "oldText",
	editsNewKey: "newText",
}

type codeWhaleSession struct {
	Metadata struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Workspace string    `json:"workspace"`
		Parent    string    `json:"parent_session_id"`
	} `json:"metadata"`
	Messages []codeWhaleMessage `json:"messages"`
}

type codeWhaleMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// CodeWhaleWorkspace is the workspace a saved session was worked in, from its
// metadata. "" when the file names none (#4362).
func CodeWhaleWorkspace(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Metadata struct {
			Workspace string `json:"workspace"`
		} `json:"metadata"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return ""
	}
	return doc.Metadata.Workspace
}

func LoadCodeWhale() []model.Session {
	return parseFiles(CodeWhaleSessionFiles(), ParseCodeWhaleFile)
}

// ParseCodeWhaleFile reads one saved session.
func ParseCodeWhaleFile(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc codeWhaleSession
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return parseCodeWhaleDoc(path, doc), nil
}

// parseCodeWhaleDoc reads a decoded session; a compaction capture hands over
// the session file's metadata with the messages a compaction saved.
func parseCodeWhaleDoc(path string, doc codeWhaleSession) []model.Session {
	id := doc.Metadata.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	s := model.Session{
		Harness: "codewhale", ID: id, Path: path,
		Title: firstLineTrim(doc.Metadata.Title),
	}
	if doc.Metadata.Workspace != "" {
		s.Project = projectName(doc.Metadata.Workspace)
	}
	if doc.Metadata.Parent != "" {
		// `codewhale fork` writes the source id here, and this file is the only
		// place that edge exists — without it a fork reads as a session of its
		// own and its parent's context never reaches it.
		s.Kind = "subagent"
		s.Parent = doc.Metadata.Parent
	}
	failed := codeWhaleFailedCalls(doc.Messages)
	start := doc.Metadata.CreatedAt
	if start.IsZero() {
		start = doc.Metadata.UpdatedAt
	}
	exits := commandExits{}
	for i, m := range doc.Messages {
		// One millisecond per record off the session's own start: the file
		// stores no per-message time, and a single stamp for the whole session
		// let two identical turns collapse into one stored record (#3333).
		ts := start.Add(time.Duration(i) * time.Millisecond)
		switch m.Role {
		case "user", "assistant", "assistant_interrupted":
		default:
			// system and developer are the harness talking to itself —
			// compaction summaries, branch summaries, sub-agent framing, by
			// its own Role documentation. Never the person's words.
			continue
		}
		if m.Role == "user" {
			if summary, ok := codeWhaleCheckpoint(m.Content); ok {
				if summary != "" {
					s.Touch(ts)
					s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: summary, Time: ts})
				}
				continue
			}
		}
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		if text := stripCodeWhaleTurnMeta(clineContentText(m.Content)); text != "" {
			s.Touch(ts)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: ts})
		}
		from := len(s.Messages)
		for _, rec := range codeWhaleWorkRecords(m.Content, ts) {
			s.Touch(ts)
			s.Messages = append(s.Messages, rec)
		}
		for _, rec := range codeWhalePatchRecords(m.Content, failed, doc.Metadata.Workspace, ts) {
			s.Touch(ts)
			s.Messages = append(s.Messages, rec)
		}
		var blocks []any
		if bytes.Contains(m.Content, []byte(`"tool_`)) && json.Unmarshal(m.Content, &blocks) == nil {
			joinResultExits(s.Messages, from, blocks, codeWhaleDialect, exits, codeWhaleExitCode)
		}
	}
	if !doc.Metadata.UpdatedAt.IsZero() {
		s.Touch(doc.Metadata.UpdatedAt)
	}
	if len(s.Messages) == 0 {
		return nil
	}
	return []model.Session{s}
}

// stripCodeWhaleTurnMeta drops the <turn_meta> block CodeWhale 0.10.0 saves as
// a text block of every user message: the date, the workspace, the permission
// posture and the files it thinks matter. It is the harness talking, and
// indexed as the person's words it made every turn match every other.
func stripCodeWhaleTurnMeta(text string) string {
	for {
		start := strings.Index(text, "<turn_meta>")
		if start < 0 {
			return strings.TrimSpace(text)
		}
		end := strings.Index(text[start:], "</turn_meta>")
		if end < 0 {
			return strings.TrimSpace(text[:start])
		}
		text = text[:start] + text[start+end+len("</turn_meta>"):]
	}
}

// codeWhaleExitCode reads a failed bash result: CodeWhale marks it is_error
// and ends it "Command exited with code N" (tools/shell.rs
// contract_bash_error_status), wrapped as "Error: …" (#4537). A timeout or a
// kill ends otherwise and gives no code.
func codeWhaleExitCode(result map[string]any) (int, bool) {
	if failed, _ := result["is_error"].(bool); !failed {
		return 0, false
	}
	return statusCode(lastLine(contentText(result["content"])), "Command exited with code ", "")
}

// codeWhaleWorkRecords turns one message's tool blocks into work records, the
// way the Cline reader does for its own dialect: the files a call named, the
// span an edit replaced, the command it ran, and what came back.
func codeWhaleWorkRecords(raw json.RawMessage, ts time.Time) []model.Message {
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	codeWhaleFoldEdits(blocks)
	codeWhaleFoldEditKeys(blocks)
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(blocks, codeWhaleDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: ts})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(blocks, codeWhaleDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: ts})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(blocks, codeWhaleDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: ts})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(blocks, codeWhaleDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: ts})
		}
	}
	if IndexToolOutput() {
		for _, body := range clineToolResults(blocks) {
			out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: ts})
		}
	}
	return out
}

// codeWhalePatchRecords reads the apply_patch calls in one message. The tool
// (tools/apply_patch.rs) takes a unified diff under `patch`, retargeted to
// `path` when that is set, or whole files under `replace` (or its deprecated
// alias `changes`) as {path, content}. Paths are relative to the workspace.
// A call that came back as an error changed nothing (#4538).
func codeWhalePatchRecords(raw json.RawMessage, failed map[string]bool, workspace string, ts time.Time) []model.Message {
	if !bytes.Contains(raw, []byte(`"apply_patch"`)) {
		return nil
	}
	var blocks []any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	resolve := func(p string) string { return resolveToolPath(p, workspace) }
	var out []model.Message
	for _, it := range blocks {
		name, in, ok := toolPart(it, codeWhaleDialect)
		if !ok || name != "apply_patch" {
			continue
		}
		if id, _ := it.(map[string]any)["id"].(string); failed[id] {
			continue
		}
		if patch, _ := in["patch"].(string); patch != "" {
			// file_path and filePath are folded onto path before it runs
			// (file.rs PATH_ALIASES).
			path := ""
			for _, k := range []string{"path", "file_path", "filePath"} {
				if path, _ = in[k].(string); path != "" {
					break
				}
			}
			files, spans, wrote := unifiedPatch(patch, path, resolve)
			out = append(out, patchRecords(files, spans, wrote, ts)...)
			continue
		}
		entries, _ := in["replace"].([]any)
		if len(entries) == 0 {
			entries, _ = in["changes"].([]any)
		}
		var files, wrote []string
		for _, e := range entries {
			m, _ := e.(map[string]any)
			path, _ := m["path"].(string)
			if path == "" || strings.ContainsAny(path, "\n\r") {
				continue
			}
			path = resolve(path)
			files = append(files, path)
			content, _ := m["content"].(string)
			if rec := WroteRecord(path, content); rec != "" {
				wrote = append(wrote, rec)
			}
		}
		out = append(out, patchRecords(files, nil, wrote, ts)...)
	}
	return out
}

// codeWhaleFailedCalls is the id of every call whose tool_result is an error.
func codeWhaleFailedCalls(msgs []codeWhaleMessage) map[string]bool {
	out := map[string]bool{}
	for _, m := range msgs {
		if !bytes.Contains(m.Content, []byte(`"is_error"`)) {
			continue
		}
		var blocks []struct {
			Type    string `json:"type"`
			ID      string `json:"tool_use_id"`
			IsError bool   `json:"is_error"`
		}
		_ = json.Unmarshal(m.Content, &blocks)
		for _, b := range blocks {
			if b.Type == "tool_result" && b.IsError {
				out[b.ID] = true
			}
		}
	}
	return out
}

// codeWhaleFoldEdits puts an edit call into the one shape the dialect reads,
// the way CodeWhale's prepare_contract_edit_input does before it runs one: an
// edits array sent as a JSON string is decoded, and a top-level
// oldText/newText pair joins edits. The transcript keeps what the model sent,
// so either shape left the edit with no span and nothing written (#4360).
func codeWhaleFoldEdits(blocks []any) {
	for _, it := range blocks {
		name, in, ok := toolPart(it, codeWhaleDialect)
		if !ok || name != "edit" {
			continue
		}
		if encoded, ok := in["edits"].(string); ok {
			var decoded []any
			if json.Unmarshal([]byte(encoded), &decoded) == nil {
				in["edits"] = decoded
			}
		}
		oldText, okOld := in["oldText"].(string)
		newText, okNew := in["newText"].(string)
		if okOld && okNew {
			edits, _ := in["edits"].([]any)
			in["edits"] = append(edits, map[string]any{"oldText": oldText, "newText": newText})
			delete(in, "oldText")
			delete(in, "newText")
		}
	}
}

// codeWhaleEditKeys are the spellings CodeWhale's edit_file folds onto its own
// search and replace before it runs (tools/file.rs EDIT_ALIASES), mapped here
// onto the dialect's old_string and new_string. search/replace is the
// canonical pair, so a call written that way had no edit and no wrote record
// (#4404).
var codeWhaleEditKeys = [][2]string{
	{"search", "old_string"}, {"replace", "new_string"},
	{"old_str", "old_string"}, {"new_str", "new_string"},
	{"oldText", "old_string"}, {"newText", "new_string"},
	{"old_text", "old_string"}, {"new_text", "new_string"},
	{"replacement", "new_string"},
}

// codeWhaleFoldEditKeys rewrites an edit_file call's arguments onto the keys
// the dialect reads. The transcript keeps what the model sent.
func codeWhaleFoldEditKeys(blocks []any) {
	for _, it := range blocks {
		name, in, ok := toolPart(it, codeWhaleDialect)
		if !ok || name != "edit_file" {
			continue
		}
		for _, k := range codeWhaleEditKeys {
			if v, ok := in[k[0]]; ok {
				if _, set := in[k[1]]; !set {
					in[k[1]] = v
				}
			}
		}
	}
}

// isCodeWhaleSession reports whether a path is one of CodeWhale's transcripts:
// a .json file directly under a session root, and not one of the sidecars it
// keeps beside them.
func isCodeWhaleSession(p string) bool {
	if filepath.Ext(p) != ".json" || codeWhaleSidecars[filepath.Base(p)] {
		return false
	}
	dir := filepath.Dir(p)
	for _, root := range CodeWhaleRoots() {
		if dir == filepath.Clean(root) {
			return true
		}
	}
	return false
}

// A compaction leaves the summary in the saved history as a user message of two
// text blocks: the note the model continues from, then a fixed provenance
// marker (compaction_checkpoint_message, crates/tui/src/compaction.rs). The
// note is a header paragraph, the summary, and a closing paragraph; 0.10.1
// opens it "Codewhale handoff note", 0.10.0 with Codex's summary prefix.
// Before 0.9.6 the summary was a message opening with its own heading.
const codeWhaleCheckpointMarker = "<!-- codewhale.compaction-checkpoint.v1 -->"

var (
	codeWhaleSummaryHeaders  = []string{"Codewhale handoff note", "Another language model started to solve this problem"}
	codeWhaleSummaryClosings = []string{"Continue the user's task from here.", "Continue the same user task from this state."}
)

// codeWhaleCheckpoint reports whether content is a compaction checkpoint, and
// its summary without the paragraphs addressed to the model.
func codeWhaleCheckpoint(content json.RawMessage) (string, bool) {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil || len(blocks) == 0 {
		return "", false
	}
	if len(blocks) == 2 && blocks[0].Type == "text" && blocks[1].Type == "text" &&
		strings.TrimSpace(blocks[1].Text) == codeWhaleCheckpointMarker {
		text := strings.TrimSpace(blocks[0].Text)
		for _, h := range codeWhaleSummaryHeaders {
			if strings.HasPrefix(text, h) {
				if _, rest, ok := strings.Cut(text, "\n\n"); ok {
					text = rest
				}
				break
			}
		}
		for _, c := range codeWhaleSummaryClosings {
			if i := strings.LastIndex(text, "\n\n"+c); i >= 0 {
				text = text[:i]
				break
			}
		}
		return strings.TrimSpace(text), true
	}
	if len(blocks) == 1 && blocks[0].Type == "text" {
		text := strings.TrimSpace(blocks[0].Text)
		if strings.HasPrefix(text, "## 📋 Conversation Summary (Auto-Generated)") ||
			(strings.HasPrefix(text, "## Pinned Facts (User Anchors)") && strings.Contains(text, "## 📋 Conversation Summary (Auto-Generated)")) {
			return text, true
		}
	}
	return "", false
}
