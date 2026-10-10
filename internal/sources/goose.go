package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GooseDataDir is where goose keeps its data on this platform: the first of
// GooseDataDirs that exists, or the one goose itself would write to when none
// does. install --auto and doctor key on it, so naming a candidate that is not
// on disk skipped goose on a mac that had used it (#4267).
func GooseDataDir() string {
	dirs := GooseDataDirs()
	for _, dir := range dirs {
		if dirExists(dir) {
			return dir
		}
	}
	return dirs[0]
}

// GooseDataDirs are the data roots a goose install can have, the current one
// first. goose resolves its own directories through etcetera's
// choose_app_strategy with author and top-level domain "Block"
// (crates/goose/src/config/paths.rs). That is the Windows strategy on Windows
// and XDG everywhere else, macOS included: the Apple layout is
// choose_native_strategy, which goose does not call. goose 1.46 on a mac keeps
// its sessions in `~/.local/share/goose` (#4267); #3642 read it the other way.
//
// The other locations stay as candidates rather than as the answer: an install
// with a store under one of them keeps being read. Every directory that exists
// is read.
func GooseDataDirs() []string {
	// GOOSE_PATH_ROOT relocates config, data and state together; a user who
	// sets it has every session under it and none where we would look. goose
	// takes it, and XDG_DATA_HOME, only when absolute (#4285).
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return []string{filepath.Join(root, "data")}
	}
	xdg := filepath.Join(Home(), ".local", "share")
	if v := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(v) {
		xdg = v
	}
	var out []string
	add := func(paths ...string) {
		for _, path := range paths {
			if path == "" {
				continue
			}
			path = filepath.Clean(path)
			seen := false
			for _, existing := range out {
				if existing == path {
					seen = true
					break
				}
			}
			if !seen {
				out = append(out, path)
			}
		}
	}
	xdgRoots := []string{filepath.Join(xdg, "goose"), filepath.Join(xdg, "Block", "goose")}
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(Home(), "AppData", "Roaming")
		}
		add(filepath.Join(appdata, "Block", "goose", "data"))
		// An older goose wrote XDG whatever the platform.
		add(xdgRoots...)
	case "darwin":
		support := filepath.Join(Home(), "Library", "Application Support")
		add(xdgRoots[0], filepath.Join(support, "Block", "goose"), filepath.Join(support, "goose"), xdgRoots[1])
	default:
		add(xdgRoots...)
	}
	return out
}

// GooseRoot is the session-reading root; DEJA_GOOSE_ROOT overrides it without
// affecting where Goose itself stores data.
func GooseRoot() string { return EnvPath("DEJA_GOOSE_ROOT", GooseDataDir()) }

// GooseRoots are the data roots to read. DEJA_GOOSE_ROOT names one and then it
// is the whole answer, the way DEJA_CODEX_ROOT is for Codex.
func GooseRoots() []string {
	if p := os.Getenv("DEJA_GOOSE_ROOT"); p != "" {
		return []string{p}
	}
	return GooseDataDirs()
}

// GooseSessionsDirs are the session directories of every candidate root.
func GooseSessionsDirs() []string {
	roots := GooseRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, filepath.Join(root, "sessions"))
	}
	return out
}

// GooseDB is the SQLite session store used by Goose >= 1.10.0: the first one
// that exists, or the primary root's when none does, so a caller reporting
// where it looked still names a directory.
func GooseDB() string {
	dbs := GooseDBs()
	for _, db := range dbs {
		if fileExists(db) {
			return db
		}
	}
	return dbs[0]
}

// GooseDBs are the SQLite stores of every candidate root.
func GooseDBs() []string {
	if p := os.Getenv("DEJA_GOOSE_DB"); p != "" {
		return []string{p}
	}
	dirs := GooseSessionsDirs()
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, filepath.Join(dir, "sessions.db"))
	}
	return out
}

func gooseJSONLFiles() []string {
	var out []string
	for _, dir := range GooseSessionsDirs() {
		out = append(out, walkFiles(dir, func(p string) bool {
			return strings.HasSuffix(p, ".jsonl") && filepath.Base(p) != "sessions.db"
		})...)
	}
	return out
}

// GooseJSONLFiles lists legacy JSONL session files (pre-1.10.0 storage).
func GooseJSONLFiles() []string { return gooseJSONLFiles() }

// GooseSessionFiles lists legacy JSONL sessions and the SQLite store when present.
func GooseSessionFiles() []string {
	out := gooseJSONLFiles()
	for _, db := range GooseDBs() {
		if fi, err := os.Stat(db); err == nil && fi.Size() > 0 {
			out = append(out, db)
		}
	}
	return out
}

func LoadGoose() []model.Session {
	ss := parseFiles(gooseJSONLFiles(), ParseGooseFile)
	for _, db := range GooseDBs() {
		dbSS, _ := ParseGooseDB(db)
		ss = append(ss, dbSS...)
	}
	return ss
}

func ParseGooseFile(path string) ([]model.Session, error) {
	return parseGooseFileFromOffset(path, 0)
}

func ParseGooseFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseGooseFileFromOffset(path, offset)
}

func parseGooseFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "goose",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	exits := commandExits{}
	err := scanJSONLWithHeaderFromOffsetFunc(path, offset, headerLookahead, isGooseHeader, func(m map[string]any) {
		role, hasRole := m["role"].(string)
		if !hasRole {
			applyGooseHeader(&s, m)
			return
		}
		if role != "user" && role != "assistant" {
			return
		}
		t := parseTimeAny(m["created"])
		if t.IsZero() {
			t = parseTimeAny(m["timestamp"])
		}
		parts := gooseParts(m["content"])
		if parts.empty() {
			return
		}
		s.Touch(t)
		appendGooseParts(&s, role, t, parts, exits)
	})
	if s.Project == "" {
		s.Project = "goose"
	}
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// isGooseHeader tells the session's metadata line from its messages, which
// all carry a role.
func isGooseHeader(m map[string]any) bool {
	_, hasRole := m["role"]
	return !hasRole
}

// applyGooseHeader reads the session's own line: the header goose writes first,
// carrying the id, the description and the directory the work happened in.
func applyGooseHeader(s *model.Session, m map[string]any) {
	if id, _ := m["id"].(string); id != "" {
		s.ID = id
	}
	if desc, _ := m["description"].(string); strings.TrimSpace(desc) != "" {
		s.Title = strings.TrimSpace(desc)
	}
	if wd, _ := m["working_dir"].(string); wd != "" {
		s.Project = projectName(wd)
	}
	s.Touch(parseTimeAny(m["created_at"]))
	s.Touch(parseTimeAny(m["updated_at"]))
}

func gooseText(v any) string {
	switch x := v.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(x), &parsed) == nil {
			return textFromContent(parsed)
		}
		return strings.TrimSpace(x)
	default:
		return textFromContent(v)
	}
}

// goosePartsOf is what deja indexes out of one row's content array.
type goosePartsOf struct {
	speech, toolOut string
	commands, paths []string
	// edits and wrote are the replaced and the written side of the file
	// changes the row asked for (#4265).
	edits, wrote []string
	// callIDs is the request each command came from, one per command, and
	// exits the code each response in the row reported, by request.
	callIDs []string
	exits   map[string]int
}

func (p goosePartsOf) empty() bool {
	return p.speech == "" && p.toolOut == "" && len(p.commands) == 0 && len(p.paths) == 0 &&
		len(p.edits) == 0 && len(p.wrote) == 0
}

// appendGooseParts files one row's content under the roles deja indexes, the
// way the Claude reader does: speech under the speaker, tool output under the
// role that marks it as a printout, and the command, the paths and the edits as
// their own records so `how`, `blame`, `restore` and the fix pairs can find
// them.
//
// exits joins a command to its response, which arrives on a later row: the
// shell tool reports how the command ended there and nowhere else (#4496).
func appendGooseParts(s *model.Session, role string, t time.Time, p goosePartsOf, exits commandExits) {
	if p.speech != "" {
		s.Messages = append(s.Messages, model.Message{Role: role, Text: capParsedMessage(p.speech), Time: t})
	}
	if p.toolOut != "" {
		s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(p.toolOut), Time: t})
	}
	if IndexToolPaths() && len(p.paths) > 0 {
		s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(p.paths, "\n"), Time: t})
	}
	if IndexWrites() {
		for _, w := range p.wrote {
			s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range p.edits {
			s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	for i, cmd := range p.commands {
		if exits != nil && p.callIDs[i] != "" {
			exits[p.callIDs[i]] = append(exits[p.callIDs[i]], len(s.Messages))
		}
		s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd, Time: t})
	}
	for id, code := range p.exits {
		exits.stamp(s.Messages, id, "", code)
	}
}

// gooseDialect is the developer extension's editor since goose folded it into
// the core (goose 1.27): `edit` takes path, before and after, `write` takes
// path and content (crates/goose/src/agents/platform_extensions/developer/
// edit.rs). The call is stored under the bare name, with the extension in
// `_meta`.
var gooseDialect = toolDialect{
	pathKey:   "path",
	editTools: map[string]bool{"edit": true, "write": true},
	oldKey:    "before",
	newKey:    "after",
}

// gooseTextEditorDialect is the text_editor tool the goose-mcp developer
// extension had before that, stored as `developer__text_editor`: str_replace
// takes old_str and new_str, write takes file_text, insert takes new_str.
var gooseTextEditorDialect = toolDialect{
	pathKey:    "path",
	editTools:  map[string]bool{"text_editor": true},
	oldKey:     "old_str",
	newKey:     "new_str",
	contentKey: "file_text",
}

// gooseTextEditorArgs keeps the text arguments the call's command reads:
// old_str and new_str for str_replace, new_str for insert, file_text for
// write. A view or an undo_edit carrying stray ones changed nothing, and
// text_editor has no `edits` list for the shared dialect reader to find.
func gooseTextEditorArgs(args map[string]any) map[string]any {
	keep := map[string][]string{
		"str_replace": {"old_str", "new_str"},
		"insert":      {"new_str"},
		"write":       {"file_text"},
	}[str(args["command"])]
	// A diff on str_replace is applied instead of old_str and new_str, and
	// gooseTextEditorDiff reads it (#4287).
	if str(args["command"]) == "str_replace" && str(args["diff"]) != "" {
		keep = nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if k != "old_str" && k != "new_str" && k != "file_text" && k != "edits" {
			out[k] = v
		}
	}
	for _, k := range keep {
		if v, ok := args[k]; ok {
			out[k] = v
		}
	}
	return out
}

// gooseTextEditorDiff reads the unified diff goose 1.10–1.25 takes on
// text_editor's str_replace, which its schema calls the preferred way to edit
// (#4287): the files it names, the spans each hunk removed and the lines it
// added.
//
// goose applies the diff under a base directory (text_editor.rs apply_diff):
// the call's path when that is a directory, its parent when it is a file, and
// with a header's leading directories that repeat the base's last ones taken
// off the base (adjust_base_dir_for_overlap). goose asks the disk which the
// path is; the record has only names, so a path whose last element is one of
// the diff's files, or has an extension, is taken for a file.
func gooseTextEditorDiff(callPath, diff string) (files, spans, wrote []string) {
	if strings.ContainsAny(callPath, "\n\r") {
		return nil, nil, nil
	}
	sep := "/"
	if !strings.Contains(callPath, "/") && strings.Contains(callPath, `\`) {
		sep = `\`
	}
	base := strings.Split(slashed(callPath), "/")
	last := base[len(base)-1]
	isFile := path.Ext(last) != ""
	for _, line := range strings.Split(diff, "\n") {
		if h, ok := strings.CutPrefix(line, "+++ "); ok && path.Base(slashed(unifiedDiffPath(h))) == last {
			isFile = true
		}
	}
	// A bare relative file name is under goose's working directory, which the
	// record does not hold, so its base is empty rather than the file.
	if isFile {
		base = base[:len(base)-1]
	}
	resolve := func(p string) string {
		if callPath == "" || isAbsolutePath(p) {
			return p
		}
		file := strings.Split(slashed(p), "/")
		dir := base
		for k := min(len(dir), len(file)); k > 0; k-- {
			if slices.Equal(file[:k], dir[len(dir)-k:]) {
				dir = dir[:len(dir)-k]
				break
			}
		}
		return strings.Join(append(slices.Clone(dir), file...), sep)
	}
	return unifiedPatch(diff, "", resolve)
}

// gooseParts splits a goose content array into the things deja indexes
// separately. goose stores far more than speech in the same column — measured
// against goose 1.48.0 by importing a transcript and reading the row back:
//
//	toolRequest  {"type":"toolRequest","id":..,"toolCall":{"status":"success",
//	              "value":{"name":"Bash","arguments":{"command":"go build ./..."}}}}
//	toolResponse {"type":"toolResponse","id":..,"toolResult":{"status":"success",
//	              "value":{"resultType":"complete","content":[{"type":"text","text":".."}],
//	              "isError":false}}}
//
// Only `text` parts were read, so a goose store contributed no commands and no
// tool output at all: `how` had nothing to answer from, and friction and the
// fix pairs never saw the error a build printed. Every other harness that
// records structured tool output has had this since the roles were named.
//
// The response arrives on a `user` row — goose files a tool result as the
// user's turn, the same way Claude Code does — so a row that holds only
// responses is tool output rather than something a person said.
func gooseParts(v any) goosePartsOf {
	items, ok := gooseContentArray(v)
	if !ok {
		return goosePartsOf{speech: gooseText(v)}
	}
	var p goosePartsOf
	var say, out []string
	// The editor calls in the shape the dialect readers take, so the edit and
	// wrote records come out the way they do for every other harness.
	var calls []any
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "text":
			if t := withoutGooseTurnContext(str(m["text"])); t != "" {
				say = append(say, t)
			}
		case "toolRequest":
			name, args := gooseToolCall(m)
			if cmd := strings.TrimSpace(str(args["command"])); cmd != "" && worthIndexing(cmd) {
				p.commands = append(p.commands, "$ "+cmd)
				p.callIDs = append(p.callIDs, str(m["id"]))
			}
			// The editor tools name the file they are about to change, which is
			// what blame reads. `path` is what goose's own editor takes; a tool
			// from an extension may spell it either way.
			for _, key := range []string{"path", "file_path"} {
				if pth := strings.TrimSpace(str(args[key])); pth != "" {
					p.paths = append(p.paths, pth)
					break
				}
			}
			if args != nil {
				tool := strings.TrimPrefix(name, "developer__")
				if tool == "text_editor" {
					if diff := str(args["diff"]); diff != "" && str(args["command"]) == "str_replace" {
						files, spans, wrote := gooseTextEditorDiff(strings.TrimSpace(str(args["path"])), diff)
						for _, f := range files {
							if f != strings.TrimSpace(str(args["path"])) {
								p.paths = append(p.paths, f)
							}
						}
						p.edits = append(p.edits, spans...)
						p.wrote = append(p.wrote, wrote...)
					}
					args = gooseTextEditorArgs(args)
				}
				calls = append(calls, map[string]any{
					"type": "tool_use", "name": tool, "input": args,
				})
			}
		case "toolResponse":
			if t := gooseToolResult(m); t != "" {
				out = append(out, t)
			}
			if code, ok := gooseExitCode(m); ok && str(m["id"]) != "" {
				if p.exits == nil {
					p.exits = map[string]int{}
				}
				p.exits[str(m["id"])] = code
			}
		}
	}
	for _, d := range []toolDialect{gooseDialect, gooseTextEditorDialect} {
		p.edits = append(p.edits, editSpansIn(calls, d)...)
		p.wrote = append(p.wrote, wroteRecordsIn(calls, d)...)
	}
	p.speech, p.toolOut = strings.Join(say, "\n"), strings.Join(out, "\n")
	return p
}

// withoutGooseTurnContext removes the block goose injects ahead of a turn —
// the clock, the working directory and the standing task list, wrapped in
// <turn-context>. goose writes it as a message of the user's own role, so
// every turn on a real store added one and deja indexed it as something the
// person said. Stripped rather than dropped whole, because a turn that carries
// both the envelope and real words must keep the words.
func withoutGooseTurnContext(text string) string {
	for {
		open := strings.Index(text, "<turn-context>")
		if open < 0 {
			break
		}
		close := strings.Index(text[open:], "</turn-context>")
		if close < 0 {
			text = text[:open]
			break
		}
		text = text[:open] + text[open+close+len("</turn-context>"):]
	}
	return strings.TrimSpace(text)
}

// gooseContentArray unwraps the column, which holds the array either as JSON
// text or already decoded, depending on how it was read.
func gooseContentArray(v any) ([]any, bool) {
	switch x := v.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(x), &parsed) != nil {
			return nil, false
		}
		items, ok := parsed.([]any)
		return items, ok
	case []any:
		return x, true
	}
	return nil, false
}

func gooseToolCall(m map[string]any) (string, map[string]any) {
	call, _ := m["toolCall"].(map[string]any)
	value, _ := call["value"].(map[string]any)
	args, _ := value["arguments"].(map[string]any)
	return str(value["name"]), args
}

// gooseToolResult reads the text a tool produced, from either side of the
// status: an error carries what went wrong, which is the half friction and the
// fix pairs are built on.
func gooseToolResult(m map[string]any) string {
	res, _ := m["toolResult"].(map[string]any)
	if value, ok := res["value"].(map[string]any); ok {
		if t := textFromContent(value["content"]); t != "" {
			return t
		}
	}
	if e, ok := res["error"].(map[string]any); ok {
		return strings.TrimSpace(str(e["message"]))
	}
	if t := strings.TrimSpace(str(res["error"])); t != "" {
		return t
	}
	return ""
}

// gooseExitCode is how a shell command ended, from its response. goose's
// shell tool (1.46) puts the code in structuredContent.exit_code, and on a
// failure also ends the text with "Command exited with code N"; a response
// that says neither is left unknown.
func gooseExitCode(m map[string]any) (int, bool) {
	res, _ := m["toolResult"].(map[string]any)
	value, _ := res["value"].(map[string]any)
	if sc, ok := value["structuredContent"].(map[string]any); ok {
		if n, ok := piExitCode(sc["exit_code"]); ok {
			return n, true
		}
	}
	if failed, _ := value["isError"].(bool); failed {
		return statusCode(lastLine(textFromContent(value["content"])), "Command exited with code ", "")
	}
	return 0, false
}

// gooseTypeFilter keeps out of recall what goose wrote for itself: Goose
// stores subagent turns and scheduled runs in the same table as the reader's
// own sessions. The column exists only on newer stores, and naming it on an
// older one fails the whole query, so it is probed rather than assumed.
//
// Stated as what to exclude rather than what to keep. #2874 named the three
// types a person reaches goose through today, which fixed the report; written
// that way round, a type goose adds next is dropped silently and nothing says
// why — the failure that produced #2873 in the first place. So: a type is a
// person's work until goose is known to write it for itself.
func gooseTypeFilter(db string) string {
	out, err := sqliteOutput(db, "pragma table_info(sessions)")
	if err != nil || !bytes.Contains(out, []byte("session_type")) {
		return ""
	}
	quoted := make([]string, 0, len(gooseMachineTypes))
	for _, t := range gooseMachineTypes {
		quoted = append(quoted, "'"+t+"'")
	}
	return " and (s.session_type is null or s.session_type not in (" + strings.Join(quoted, ",") + "))"
}

// gooseMachineTypes are the session types goose writes for its own work rather
// than the reader's: a subagent it spawned, spelled both ways across versions,
// and a run its scheduler started.
//
// Read off goose's own enum — user, scheduled, sub_agent, hidden, terminal,
// gateway, acp — rather than guessed. `hidden` is not on this list although
// the name suggests it: goose writes it for `goose run --no-session`, the
// reader working without keeping a session, and for two wizards that store no
// conversation and fall out of the role join anyway. `gateway` is a person
// reaching goose from a chat app. Both are history someone made.
var gooseMachineTypes = []string{"subagent", "sub_agent", "scheduled"}

func ParseGooseDB(db string) ([]model.Session, error) {
	return parseGooseDBWhere(db, "", 0)
}

func ParseGooseDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return parseGooseDBWhere(db, "", 0)
	}
	// A second back, as crush, hermes, kiro and devin look: both sides are
	// compared in whole seconds, and a turn stored in the watermark's own
	// second fails a strict > for good.
	t = t.Add(-time.Second)
	sec := t.Unix()
	rfc := sqlEscape(t.UTC().Format(time.RFC3339Nano))
	// Compared through datetime() because the two sides are written in
	// different formats: a real store keeps updated_at as sqlite's own
	// "2026-07-27 15:29:47", and a space sorts below the T of an RFC3339
	// string, so a plain text comparison missed every session touched later the
	// same day — including a turn goose stored without a timestamp of its own,
	// which is reachable only through its session — and matched every session
	// touched on a later date, handing back all of it (#2030).
	//
	// A matching session comes back whole, which is work repeated on every pass
	// over an active store (#2030) — but it is also what keeps the session
	// whole in the index: a partial return would replace what goose already
	// had there (#2033).
	where := fmt.Sprintf(" and (m.created_timestamp > %d or datetime(s.updated_at) > datetime('%s'))", sec, rfc)
	return parseGooseDBWhere(db, where, 0)
}

func parseGooseDBWhere(db, where string, limit int) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	// json_object, one object per row, rather than the sqlite3 shell's -json
	// formatter: that one is quadratic in the number of characters it escapes,
	// and a message blob is mostly quotes and backslashes. See opencode.go.
	q := `select json_object('id',cast(s.id as text),'working_dir',cast(s.working_dir as text),` +
		`'description',cast(s.description as text),'created_at',s.created_at,'updated_at',s.updated_at,` +
		`'role',cast(m.role as text),'content_json',cast(m.content_json as text),` +
		`'created_timestamp',m.created_timestamp) ` +
		`from sessions s join messages m on m.session_id=s.id ` +
		`where m.role in ('user','assistant')` + gooseTypeFilter(db) + where +
		` order by s.id,m.created_timestamp,m.id` + lim
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	by := map[string]*model.Session{}
	exits := map[string]commandExits{}
	rows := 0
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		id, _ := r["id"].(string)
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			dir, _ := r["working_dir"].(string)
			desc, _ := r["description"].(string)
			s = &model.Session{
				Harness: "goose",
				ID:      id,
				Project: projectName(dir),
				Path:    db,
				Title:   strings.TrimSpace(desc),
				Started: parseTimeAny(r["created_at"]),
				Updated: parseTimeAny(r["updated_at"]),
			}
			if s.Project == "" {
				s.Project = "goose"
			}
			by[id] = s
		}
		role := str(r["role"])
		t := parseTimeAny(r["created_timestamp"])
		if t.IsZero() {
			t = s.Updated
		}
		parts := gooseParts(r["content_json"])
		if parts.empty() {
			continue
		}
		s.Touch(t)
		if exits[id] == nil {
			exits[id] = commandExits{}
		}
		appendGooseParts(s, role, t, parts, exits[id])
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			return nil, fmt.Errorf("goose: query failed, the store schema may have changed: %w", err)
		}
		return nil, err
	}
	var out []model.Session
	for _, s := range by {
		if len(s.Messages) == 0 {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// GooseStoreLacks reports whether the goose store a session was read from no
// longer has it — deleted in goose, which leaves `goose session --resume`
// answering "no such session exists" (#4271). False whenever that cannot be
// told: no file, no sessions table, or a read that fails.
func GooseStoreLacks(db, id string) bool {
	if !nonEmptyFile(db) {
		return false
	}
	query := func(q string) (string, bool) {
		cmd, stop := sqliteReadCmd(db, q)
		defer stop()
		b, err := cmd.Output()
		return strings.TrimSpace(string(b)), err == nil
	}
	if names, ok := query(`select name from sqlite_master where type='table' and name='sessions'`); !ok || names == "" {
		return false
	}
	n, ok := query(fmt.Sprintf(`select count(*) from sessions where id='%s'`, sqlEscape(id)))
	return ok && n == "0"
}
