package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Muse Code is Meta's terminal agent. It keeps one directory per session,
// sharded by the day it started, on every platform:
//
//	${XDG_DATA_HOME:-~/.local/share}/muse/sessions/YYYY/MM/DD/<uuid>/session.jsonl
//
// The log is event-sourced. Each line is one record, {schema_version, id,
// stream, sequence, recorded_at, record_type, payload_type, payload} with
// recorded_at in microseconds, or a `retained_frame` holding several records as
// JSON strings under children[].record_json. The conversation is the `run`
// events of payload_type "runtime.session":
//
//   - started with a prompt is the person's turn; a task start has the same
//     kind and no prompt, and a scheduled run's prompt is empty;
//   - assistant_message_committed carries the reply;
//   - assistant_tool_calls_committed carries tool_calls[{call_id, name, args}],
//     args a JSON string, and tool_result_batch_committed the results
//     [{tool_call_id, text}]; bash's text is itself a JSON object.
//
// The workspace is runtime.session.metadata's record.workspace_root, or
// record.cwd in runtime.session.route_facts, or record.workspace_root in
// session.workspace_branch.observed; the title, session.name.changed.
// Shapes from AstroQore/agent-session-kit's MuseSessionAdapter (#21, read off
// muse 1.3.0) and specstory's musecode provider; tokscale reads the same path
// (#1349).
//
// A subagent's log is the same format at <uuid>/subagent/<child>/session.jsonl.
// The parent's file also holds the child's task-stream records under another
// stream id; those are left out, so a child's objective is not read as
// something the person typed. Child logs are read as their task, edits and
// last turns, whole with DEJA_INCLUDE_SUBAGENTS=1 and not at all with =0
// (#5301), and only the children that name a workspace: the rest are Muse's
// observers (#4711).

// MuseRoots is every session directory: DEJA_MUSE_ROOTS (a path list) when
// set, else $XDG_DATA_HOME/muse/sessions where XDG_DATA_HOME is absolute, else
// ~/.local/share/muse/sessions.
func MuseRoots() []string {
	if list := os.Getenv("DEJA_MUSE_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(xdg) {
		return []string{filepath.Join(xdg, "muse", "sessions")}
	}
	return []string{filepath.Join(Home(), ".local", "share", "muse", "sessions")}
}

// MuseRoot is the first root, the one doctor and sources name.
func MuseRoot() string {
	if roots := MuseRoots(); len(roots) > 0 {
		return roots[0]
	}
	return ""
}

const museLogName = "session.jsonl"

// MuseSubagentFile reports whether p is a child's log,
// <session>/subagent/<child>/session.jsonl.
func MuseSubagentFile(p string) bool {
	return filepath.Base(p) == museLogName && filepath.Base(filepath.Dir(filepath.Dir(p))) == "subagent"
}

func underMuseRoot(p string) bool {
	for _, root := range MuseRoots() {
		if strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isMuseSession(p string) bool {
	return filepath.Base(p) == museLogName && underMuseRoot(p)
}

// MuseSessionFiles lists the session logs, children unless
// DEJA_INCLUDE_SUBAGENTS=0.
func MuseSessionFiles() []string {
	subagents := !SubagentsOff()
	var out []string
	for _, root := range MuseRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			return filepath.Base(p) == museLogName && (subagents || !MuseSubagentFile(p))
		})...)
	}
	return out
}

// MuseSidecarFiles is everything else in the store — cron.db, goals.db, the
// tool-outputs spill directory, the session index — so doctor places it rather
// than counting it as transcripts it could not read.
func MuseSidecarFiles() []string {
	var out []string
	for _, root := range MuseRoots() {
		out = append(out, walkFiles(root, func(p string) bool { return filepath.Base(p) != museLogName })...)
	}
	return out
}

func LoadMuse() []model.Session { return parseFiles(MuseSessionFiles(), ParseMuseFile) }

// museDialect is Muse's tool vocabulary: read_file, write_file and
// edit_file take `path`, edit_file replaces `find` with `replace`, and bash
// takes `command`.
var museDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"read_file": true, "write_file": true, "edit_file": true},
	shellTool: "bash",
	editTools: map[string]bool{"write_file": true, "edit_file": true},
	oldKey:    "find",
	newKey:    "replace",
}

// museRecords is the records one line holds: itself, or a retained frame's
// children.
func museRecords(m map[string]any) []map[string]any {
	children, ok := m["children"].([]any)
	if !ok {
		return []map[string]any{m}
	}
	var out []map[string]any
	for _, c := range children {
		cm, _ := c.(map[string]any)
		raw, _ := cm["record_json"].(string)
		if raw == "" {
			continue
		}
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		var rec map[string]any
		if d.Decode(&rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// ParseMuseFile reads one session log.
func ParseMuseFile(path string) ([]model.Session, error) {
	ss, _, err := parseMuseLog(path)
	return ss, err
}

// parseMuseLog is ParseMuseFile, also returning the workspace the session
// names, read from the session's own stream only.
func parseMuseLog(path string) ([]model.Session, string, error) {
	dir := filepath.Dir(path)
	s := model.Session{Harness: "muse", ID: filepath.Base(dir), Path: path}
	child := MuseSubagentFile(path)
	if child {
		s.Kind = "subagent"
		s.Parent = filepath.Base(filepath.Dir(filepath.Dir(dir)))
	}
	streamID := ""
	var ws museWorkspaceFacts
	exits := commandExits{}
	err := scanJSONL(path, func(line map[string]any) {
		for _, rec := range museRecords(line) {
			stream, _ := rec["stream"].(map[string]any)
			sid, _ := stream["id"].(string)
			if streamID == "" {
				if kind, _ := stream["kind"].(string); kind == "session" && sid != "" {
					streamID = sid
				}
			}
			// Another stream's records — a child's task run logged in the
			// parent — are not this conversation.
			if sid != "" && streamID != "" && sid != streamID {
				continue
			}
			if ws.take(rec) {
				continue
			}
			payload, _ := rec["payload"].(map[string]any)
			switch rec["payload_type"] {
			case "session.name.changed":
				if v, _ := payload["new_name"].(string); strings.TrimSpace(v) != "" {
					s.Title = firstLineTrim(v)
				}
				continue
			case "runtime.session":
			default:
				continue
			}
			if kind, _ := payload["kind"].(string); kind != "run" {
				continue
			}
			ev, _ := payload["event"].(map[string]any)
			t := parseTimeAny(rec["recorded_at"])
			switch ev["kind"] {
			case "started":
				// A task start shares the kind and has no prompt.
				if p, _ := ev["prompt"].(string); strings.TrimSpace(p) != "" {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: "user", Text: p, Time: t})
				}
			case "assistant_message_committed":
				if text, _ := ev["text"].(string); strings.TrimSpace(text) != "" {
					s.Touch(t)
					s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: t})
				}
			case "assistant_tool_calls_committed":
				s.Touch(t)
				calls := museToolCalls(ev["tool_calls"])
				from := len(s.Messages)
				s.Messages = append(s.Messages, museWorkRecords(calls, t)...)
				joinResultExits(s.Messages, from, calls, museDialect, exits, museExitCode)
			case "tool_result_batch_committed":
				s.Touch(t)
				results := museToolResults(ev["results"])
				if IndexToolOutput() {
					for _, r := range results {
						if body := strings.TrimSpace(r["content"].(string)); body != "" {
							s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: t})
						}
					}
				}
				blocks := make([]any, len(results))
				for i, r := range results {
					blocks[i] = r
				}
				joinResultExits(s.Messages, len(s.Messages), blocks, museDialect, exits, museExitCode)
			}
		}
	})
	if streamID != "" {
		if !child {
			s.ID = streamID
		} else if streamID != s.Parent {
			s.ID = streamID
		}
	}
	workspace := ws.root()
	// Muse runs a reminder observer beside every session, and a verification
	// one after tool use, as children logged like a delegated agent. Their
	// prompt is Muse's own instruction text. They are the children that name
	// no workspace: a workflow child records the one it works in, an observer
	// only reads the parent's conversation (#4711).
	if child && workspace == "" {
		return nil, "", err
	}
	s.Project = projectName(workspace)
	if len(s.Messages) == 0 {
		return nil, workspace, err
	}
	return []model.Session{s}, workspace, err
}

// museToolCalls puts the calls in the tool_use shape the shared extractors
// read. args is a JSON string; one that is not an object names no file and
// runs no command.
func museToolCalls(v any) []any {
	items, _ := v.([]any)
	var out []any
	for _, it := range items {
		c, _ := it.(map[string]any)
		name, _ := c["name"].(string)
		if name == "" {
			continue
		}
		var in map[string]any
		switch a := c["args"].(type) {
		case string:
			_ = json.Unmarshal([]byte(a), &in)
		case map[string]any:
			in = a
		}
		if in == nil {
			continue
		}
		id, _ := c["call_id"].(string)
		out = append(out, map[string]any{"type": "tool_use", "id": id, "name": name, "input": in})
	}
	return out
}

// museToolResults reads a result batch as tool_result blocks. bash answers with
// a JSON object, {command, exit_code, output, …}; its output is what the
// command printed, and its exit_code goes on the command.
func museToolResults(v any) []map[string]any {
	items, _ := v.([]any)
	var out []map[string]any
	for _, it := range items {
		r, _ := it.(map[string]any)
		text := contentText(r["text"])
		block := map[string]any{"type": "tool_result", "content": text}
		if id, _ := r["tool_call_id"].(string); id != "" {
			block["tool_use_id"] = id
		}
		if strings.HasPrefix(strings.TrimSpace(text), "{") {
			var report struct {
				ExitCode *int    `json:"exit_code"`
				Output   *string `json:"output"`
			}
			if json.Unmarshal([]byte(text), &report) == nil && report.Output != nil {
				block["content"] = *report.Output
				if report.ExitCode != nil {
					block["exit_code"] = *report.ExitCode
				}
			}
		}
		out = append(out, block)
	}
	return out
}

func museExitCode(result map[string]any) (int, bool) {
	n, ok := result["exit_code"].(int)
	return n, ok && n != 0
}

func museWorkRecords(calls []any, t time.Time) []model.Message {
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(calls, museDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, museDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, museDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(calls, museDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
	return out
}

// museWorkspaceFacts collects where a session says it ran: runtime.session.
// metadata's workspace_root, else route_facts' cwd, else the workspace_root of
// session.workspace_branch.observed, which is the only one a workflow child
// records (#4712).
type museWorkspaceFacts struct{ metadata, cwd, branch string }

// take reads rec if it is one of the three records, and reports whether it was.
func (w *museWorkspaceFacts) take(rec map[string]any) bool {
	payload, _ := rec["payload"].(map[string]any)
	inner, _ := payload["record"].(map[string]any)
	var key string
	var dst *string
	switch rec["payload_type"] {
	case "runtime.session.metadata":
		key, dst = "workspace_root", &w.metadata
	case "runtime.session.route_facts":
		key, dst = "cwd", &w.cwd
	case "session.workspace_branch.observed":
		key, dst = "workspace_root", &w.branch
	default:
		return false
	}
	if v, _ := inner[key].(string); v != "" && *dst == "" {
		*dst = v
	}
	return true
}

func (w museWorkspaceFacts) root() string {
	for _, v := range []string{w.metadata, w.cwd, w.branch} {
		if v != "" {
			return v
		}
	}
	return ""
}

// MuseWorkspace is the directory a session ran in, read from its log as
// ParseMuseFile reads it, or "" when the log names none. `muse resume` adopts
// the directory it is run from as the workspace, so this is where deja resume
// runs it (#4710).
func MuseWorkspace(path string) string {
	_, ws, _ := parseMuseLog(path)
	return ws
}

// museSessionID is the shape of a session id, the directory name it is kept
// under. Checked before the id goes into a glob.
var museSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// MuseSessionPath is the log of the session with this id, or "". Muse's hook
// payload names the session and sends transcript_path null, and the day
// directory it sits under is the day it started.
func MuseSessionPath(id string) string {
	if !museSessionID.MatchString(id) {
		return ""
	}
	for _, root := range MuseRoots() {
		if m, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", id, museLogName)); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// readMuseCompaction captures a Muse session for a PreCompact hook: the log
// found by its id, read whole as the index reads it. The id has to be the
// log's own stream, so a misplaced log is never taken for this session.
func readMuseCompaction(id string) (CompactionTranscript, error) {
	path := MuseSessionPath(id)
	if path == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: no muse session %q", ErrTranscriptIdentity, id)
	}
	info, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, err
	}
	ss, workspace, err := parseMuseLog(path)
	if err != nil {
		return CompactionTranscript{}, err
	}
	if len(ss) != 1 || ss[0].ID != id {
		return CompactionTranscript{}, fmt.Errorf("%w: %s is not session %q", ErrTranscriptIdentity, path, id)
	}
	return CompactionTranscript{
		Session: ss[0], Harness: "muse", NativeSessionID: id, Workspace: workspace,
		Path: path, Fingerprint: sessionFingerprint(ss[0]), SourceSize: info.Size(), SourceMTime: info.ModTime(),
		// Read through the parser, not by offset: nothing for the
		// compaction-to-edit metric to count against.
		MetricComplete: false,
	}, nil
}
