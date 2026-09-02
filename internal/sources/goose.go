package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GooseDataDir is Block's Goose CLI data root. On Linux it honors XDG_DATA_HOME;
// on Windows it uses %APPDATA%\Block\goose\data.
func GooseDataDir() string {
	// GOOSE_PATH_ROOT relocates config, data and state together; a user who
	// sets it has every session under it and none where we would look.
	if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
		return filepath.Join(root, "data")
	}
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "Block", "goose", "data")
		}
		return filepath.Join(Home(), "AppData", "Roaming", "Block", "goose", "data")
	}
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "goose")
		}
	}
	return filepath.Join(Home(), ".local", "share", "goose")
}

// GooseRoot is the session-reading root; DEJA_GOOSE_ROOT overrides it without
// affecting where Goose itself stores data.
func GooseRoot() string { return EnvPath("DEJA_GOOSE_ROOT", GooseDataDir()) }

func gooseSessionsDir() string { return filepath.Join(GooseRoot(), "sessions") }

// GooseDB is the SQLite session store used by Goose >= 1.10.0.
func GooseDB() string {
	if p := os.Getenv("DEJA_GOOSE_DB"); p != "" {
		return p
	}
	return filepath.Join(gooseSessionsDir(), "sessions.db")
}

func gooseJSONLFiles() []string {
	return walkFiles(gooseSessionsDir(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl") && filepath.Base(p) != "sessions.db"
	})
}

// GooseJSONLFiles lists legacy JSONL session files (pre-1.10.0 storage).
func GooseJSONLFiles() []string { return gooseJSONLFiles() }

// GooseSessionFiles lists legacy JSONL sessions and the SQLite store when present.
func GooseSessionFiles() []string {
	out := gooseJSONLFiles()
	if fi, err := os.Stat(GooseDB()); err == nil && fi.Size() > 0 {
		out = append(out, GooseDB())
	}
	return out
}

func LoadGoose() []model.Session {
	ss := parseFiles(gooseJSONLFiles(), ParseGooseFile)
	dbSS, _ := ParseGooseDB(GooseDB())
	return append(ss, dbSS...)
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
	err := scanJSONLWithHeaderFromOffset(path, offset, func(m map[string]any) {
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
		speech, toolOut, commands, paths := gooseParts(m["content"])
		if speech == "" && toolOut == "" && len(commands) == 0 && len(paths) == 0 {
			return
		}
		s.Touch(t)
		appendGooseParts(&s, role, t, speech, toolOut, commands, paths)
	})
	if s.Project == "" {
		s.Project = "goose"
	}
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
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

// appendGooseParts files one row's content under the roles deja indexes, the
// way the Claude reader does: speech under the speaker, tool output under the
// role that marks it as a printout, and the command and the paths as their own
// records so `how`, `blame` and the fix pairs can find them.
func appendGooseParts(s *model.Session, role string, t time.Time, speech, toolOut string, commands, paths []string) {
	if speech != "" {
		s.Messages = append(s.Messages, model.Message{Role: role, Text: capParsedMessage(speech), Time: t})
	}
	if toolOut != "" {
		s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(toolOut), Time: t})
	}
	if IndexToolPaths() && len(paths) > 0 {
		s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: strings.Join(paths, "\n"), Time: t})
	}
	for _, cmd := range commands {
		s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: cmd, Time: t})
	}
}

// gooseParts splits a goose content array into the four things deja indexes
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
func gooseParts(v any) (speech, toolOut string, commands, paths []string) {
	items, ok := gooseContentArray(v)
	if !ok {
		return gooseText(v), "", nil, nil
	}
	var say, out []string
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
				commands = append(commands, "$ "+cmd)
			}
			// The editor tools name the file they are about to change, which is
			// what blame reads. `path` is what goose's own editor takes; a tool
			// from an extension may spell it either way.
			for _, key := range []string{"path", "file_path"} {
				if pth := strings.TrimSpace(str(args[key])); pth != "" {
					paths = append(paths, pth)
					break
				}
			}
			_ = name
		case "toolResponse":
			if t := gooseToolResult(m); t != "" {
				out = append(out, t)
			}
		}
	}
	return strings.Join(say, "\n"), strings.Join(out, "\n"), commands, paths
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
	out, err := exec.Command("sqlite3", "-readonly", sqliteTarget(db), ".timeout 5000", "pragma table_info(sessions)").Output()
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
	// whole in the index: a partial return replaces what goose already had
	// there, where the same partial return from opencode merges (#2033).
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
	q := `select s.id,s.working_dir,s.description,s.created_at,s.updated_at,` +
		`m.role,m.content_json,m.created_timestamp ` +
		`from sessions s join messages m on m.session_id=s.id ` +
		`where m.role in ('user','assistant')` + gooseTypeFilter(db) + where +
		` order by s.id,m.created_timestamp,m.id` + lim
	cmd := exec.Command("sqlite3", "-readonly", "-json", sqliteTarget(db), ".timeout 5000", q)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		waitErr := cmd.Wait()
		if err == io.EOF {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("goose: query failed, the store schema may have changed: %w", waitErr)
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	by := map[string]*model.Session{}
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
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
		speech, toolOut, commands, paths := gooseParts(r["content_json"])
		if speech == "" && toolOut == "" && len(commands) == 0 && len(paths) == 0 {
			continue
		}
		s.Touch(t)
		appendGooseParts(s, role, t, speech, toolOut, commands, paths)
	}
	if _, err := dec.Token(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
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
