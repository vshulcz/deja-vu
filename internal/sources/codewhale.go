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
		out = append(out, walkFiles(filepath.Join(root, "checkpoints"), func(string) bool { return true })...)
	}
	return out
}

// codeWhaleDialect is what CodeWhale calls its tools. The shell tool answers to
// three names on the wire — the canonical exec_shell and the bash aliases every
// model already knows — and the file tools take `path`. apply_patch carries a
// patch rather than a replaced span, so it names a file and no edit. Since
// 0.9.6 new turns use read, write and edit instead, and edit takes
// edits[{oldText,newText}]; the older names stay for sessions saved before
// (#4360).
var codeWhaleDialect = toolDialect{
	pathKey: "path",
	pathTools: map[string]bool{
		"read": true, "write": true, "edit": true,
		"read_file": true, "write_file": true, "edit_file": true,
		"fim_edit": true, "apply_patch": true, "str_replace": true,
	},
	shellTool:  "exec_shell",
	shellTools: map[string]bool{"exec_shell": true, "bash": true, "Bash": true},
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
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
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
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		if text := clineContentText(m.Content); text != "" {
			s.Touch(ts)
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: ts})
		}
		from := len(s.Messages)
		for _, rec := range codeWhaleWorkRecords(m.Content, ts) {
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
		return nil, nil
	}
	return []model.Session{s}, nil
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
