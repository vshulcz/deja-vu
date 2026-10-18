package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CopilotHome is where Copilot CLI keeps its config and state: COPILOT_HOME
// when set, ~/.copilot otherwise. Install, doctor and the session root all
// follow it (#4240).
func CopilotHome() string {
	return EnvPath("COPILOT_HOME", filepath.Join(Home(), ".copilot"))
}

// CopilotRoot returns the GitHub Copilot CLI session-state root, overridable
// via DEJA_COPILOT_ROOT. Each session lives in its own UUID directory as an
// append-only events.jsonl.
func CopilotRoot() string {
	return EnvPath("DEJA_COPILOT_ROOT", filepath.Join(CopilotHome(), "session-state"))
}

// CopilotSessionFiles lists event logs under the Copilot session root.
func CopilotSessionFiles() []string {
	return walkFiles(CopilotRoot(), func(p string) bool {
		return filepath.Base(p) == "events.jsonl"
	})
}

// CopilotSessionFile is the event log Copilot CLI keeps for session id, or ""
// when there is none. Its hooks name the session and not the file.
func CopilotSessionFile(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ""
	}
	p := filepath.Join(CopilotRoot(), id, "events.jsonl")
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return p
}

// CopilotSidecarFiles lists the IDE's bookkeeping beside each session —
// vscode.metadata.json — which doctor counted as a transcript deja could not
// read, once per session (#3303).
func CopilotSidecarFiles() []string {
	return walkFiles(CopilotRoot(), func(p string) bool {
		// Copilot CLI 1.0.79 also keeps rewind snapshots per session
		// (rewind-file-snapshots/index.json, tracking.json): the client's own
		// state, not transcripts deja failed to read (#4233).
		// autopilot-objective.json is the goal of an autopilot run (#4476).
		return filepath.Base(p) == "vscode.metadata.json" ||
			filepath.Base(p) == "autopilot-objective.json" ||
			filepath.Base(filepath.Dir(p)) == "rewind-file-snapshots"
	})
}

// LoadCopilot loads all Copilot CLI sessions.
func LoadCopilot() []model.Session { return parseFiles(CopilotSessionFiles(), ParseCopilotFile) }

// copilotSkillContextRe is the wrapper Copilot CLI puts around a skill's body
// when it injects it as a user.message: the block is the host's, and the
// whole skill indexed as the person's words (#3305). A person's words beside
// it stay.
var copilotSkillContextRe = regexp.MustCompile(`(?s)<skill-context\b[^>]*>.*?</skill-context>`)

func copilotStripSkillContext(txt string) string {
	if !strings.Contains(txt, "<skill-context") {
		return txt
	}
	return strings.TrimSpace(copilotSkillContextRe.ReplaceAllString(txt, ""))
}

// ParseCopilotFile parses a single Copilot events.jsonl.
func ParseCopilotFile(path string) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, 0)
}

// ParseCopilotFileFromOffset parses a Copilot event log starting at a byte offset.
func ParseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseCopilotFileFromOffset(path, offset)
}

// copilotDialect is Copilot's tool vocabulary, read off events.jsonl its own
// CLI wrote. Names are lowercase where Claude's are capitalised, the file key
// is `path`, and the replaced span is `old_str` rather than `old_string` —
// reading the wrong one loses the only record of what stopped existing.
var copilotDialect = toolDialect{
	pathKey: "path",
	// view is how Copilot CLI 1.0.79 reads a file, under either model
	// family (#4491).
	pathTools: map[string]bool{"edit": true, "read": true, "write": true, "create": true, "view": true},
	shellTool: "bash",
	// The whole-file writes belong here too, or nothing a created file holds
	// is evidence it was ever in a session — and a commit that only adds
	// lines has no replaced side to fall back on (#595).
	editTools:  map[string]bool{"edit": true, "create": true, "write": true},
	oldKey:     "old_str",
	newKey:     "new_str",
	contentKey: "file_text",
}

func parseCopilotFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseCopilotScan(path, offset, func(fn func(map[string]any)) error {
		return scanJSONLFromOffset(path, offset, fn)
	})
}

// parseCopilotScan reads the records scan yields as path's, from offset.
func parseCopilotScan(path string, offset int64, scan func(func(map[string]any)) error) ([]model.Session, error) {
	s := model.Session{
		Harness: "copilot",
		ID:      filepath.Base(filepath.Dir(path)),
		Path:    path,
	}
	// Which message holds each shell command, by the call id the completion
	// event repeats: Copilot files the command and its outcome as two records.
	type cmdRef struct {
		s  *model.Session
		at []int
	}
	commandAt := map[string]cmdRef{}
	// A `task` sub-agent runs inside the parent's log, every record of it
	// stamped with agentId. It is a session of its own, as Claude Code's
	// sidechains are: read into the parent, the prompt the parent agent wrote
	// for it was the person's turn and its edits were the parent's (#5216).
	var children []*model.Session
	childByID := map[string]*model.Session{}
	// A /fork copies the source's records, its sub-agents' among them, and
	// only then says it is a fork. The copied runs belong to the source,
	// which has them under the same ids (#5217).
	copied := map[string]bool{}
	// When session.start says this session began.
	var startAt time.Time
	sessionFor := func(m map[string]any) *model.Session {
		agent, _ := m["agentId"].(string)
		if agent == "" {
			return &s
		}
		c := childByID[agent]
		if c == nil {
			c = &model.Session{Harness: "copilot", ID: agent, Path: path, Kind: "sidechain"}
			childByID[agent] = c
			children = append(children, c)
		}
		return c
	}
	// Where the session runs, which a patch's relative paths are under. An
	// incremental read starts past session.start, so it comes off the head,
	// with the id, start and project a rebuild takes from there: an index run
	// between session.start and the first prompt (a hook's refresh does) left
	// the session filed under no project for good (#5136).
	cwd := ""
	if offset > 0 {
		head := copilotReadHead(path)
		cwd = head.cwd
		if head.id != "" {
			s.ID = head.id
		}
		s.Touch(head.started)
		startAt = head.started
		if cwd != "" {
			s.Project = copilotProjectName(cwd)
		}
	}
	err := scan(func(m map[string]any) {
		typ, _ := m["type"].(string)
		data, _ := m["data"].(map[string]any)
		t := parseTimeAny(m["timestamp"])
		cur := sessionFor(m)
		// A run from before this session started is a copy: /fork writes the
		// source's records first and its note only after them, and an index
		// pass in between would file the copy under the original's id.
		if cur != &s && !startAt.IsZero() && !t.IsZero() && t.Before(startAt) {
			copied[cur.ID] = true
		}
		switch typ {
		case "subagent.started":
			if name, _ := data["agentName"].(string); name != "" {
				cur.Agent = name
			} else if name, _ := data["agentDisplayName"].(string); name != "" {
				cur.Agent = name
			}
		case "session.info":
			if kind, _ := data["infoType"].(string); kind != "fork" || cur != &s {
				return
			}
			msg, _ := data["message"].(string)
			if src := copilotForkSource(msg); src != "" && src != s.ID {
				s.Kind, s.Parent = "fork", src
				for id := range childByID {
					copied[id] = true
				}
			}
		case "session.start":
			if data == nil {
				return
			}
			if id, _ := data["sessionId"].(string); id != "" {
				s.ID = id
			}
			// The record's own time when startTime is missing: a fork's is
			// the moment it was made, so the copies still read as older.
			if startAt = parseTimeAny(data["startTime"]); startAt.IsZero() {
				startAt = t
			}
			s.Touch(startAt)
			if ctx, ok := data["context"].(map[string]any); ok {
				if c, _ := ctx["cwd"].(string); c != "" {
					cwd = c
					s.Project = copilotProjectName(cwd)
				}
			}
		case "session.compaction_complete":
			// {"success":true,"summaryContent":…} is the only record of the
			// turns the compaction dropped from the model's context (1.0.79).
			if data == nil {
				return
			}
			ok, _ := data["success"].(bool)
			txt, _ := data["summaryContent"].(string)
			if ok && strings.TrimSpace(txt) != "" {
				cur.Touch(t)
				cur.Messages = append(cur.Messages, model.Message{Role: RoleSummary, Text: txt, Time: t})
			}
		case "user.message", "assistant.message":
			if data == nil {
				return
			}
			role := "user"
			if typ == "assistant.message" {
				role = "assistant"
			}
			cur.Touch(t)
			txt, _ := data["content"].(string)
			if role == "user" {
				txt = copilotStripSkillContext(txt)
			}
			if txt != "" {
				cur.Messages = append(cur.Messages, model.Message{Role: role, Text: txt, Time: t})
			}
		case "tool.execution_start":
			// The call carries the work: which file, what it replaced, what
			// command ran. Copilot files these outside the message stream, so
			// the assistant turns that do nothing but call tools were being
			// stored as empty and the work was reachable from nothing.
			if data == nil {
				return
			}
			name, _ := data["toolName"].(string)
			// With a GPT model the only edit tool is apply_patch, a freeform
			// tool whose arguments are the patch string itself rather than an
			// object, so every file such a session changed was dropped (#4491).
			// Its paths may be relative; Copilot resolves them against the
			// session's directory.
			if body, ok := data["arguments"].(string); ok && name == "apply_patch" {
				resolve := func(p string) string { return resolveToolPath(p, cwd) }
				if records := applyPatchRecords(body, resolve, t); len(records) > 0 {
					cur.Touch(t)
					cur.Messages = append(cur.Messages, records...)
				}
				return
			}
			args, _ := data["arguments"].(map[string]any)
			if name == "" || args == nil {
				return
			}
			part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
			var records []model.Message
			if IndexToolPaths() {
				if p := toolPathsIn(part, copilotDialect); p != "" {
					records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
				}
			}
			if IndexWrites() {
				for _, w := range wroteRecordsIn(part, copilotDialect) {
					records = append(records, model.Message{Role: RoleWrote, Text: w, Time: t})
				}
			}
			if IndexEdits() {
				for _, span := range editSpansIn(part, copilotDialect) {
					records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
				}
			}
			// Every command of the call, not the last one: a dialect that
			// hands back an array would otherwise mark one of them.
			var cmdAt []int
			if IndexCommands() {
				for _, cmd := range commandsIn(part, copilotDialect) {
					cmdAt = append(cmdAt, len(records))
					records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
				}
			}
			if len(records) == 0 {
				return
			}
			cur.Touch(t)
			if id, _ := data["toolCallId"].(string); id != "" && len(cmdAt) > 0 {
				// Assigned, not appended: an id reused for a later call would
				// otherwise carry the first call's commands too, and the second
				// outcome would land on a run that had already finished
				// (review of #3369).
				at := make([]int, 0, len(cmdAt))
				for _, i := range cmdAt {
					at = append(at, len(cur.Messages)+i)
				}
				commandAt[id] = cmdRef{s: cur, at: at}
			}
			cur.Messages = append(cur.Messages, records...)
		case "tool.execution_complete":
			// Kept whether or not the call succeeded: the error a command hit
			// is exactly what a later search reaches for, and copilot's own
			// failure text was unreachable before this.
			if !IndexToolOutput() || data == nil {
				return
			}
			result, _ := data["result"].(map[string]any)
			out, _ := result["content"].(string)
			// What the command did, which Copilot does not put in `success`:
			// that field is true on every completion in a real store, failed
			// runs included, and the code lives in the telemetry and in a
			// trailer inside the output (#3369).
			if code := copilotExitCode(data, out); code > 0 {
				if id, _ := data["toolCallId"].(string); id != "" {
					if c := commandAt[id]; c.s != nil {
						for _, i := range c.at {
							if i < len(c.s.Messages) {
								c.s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
							}
						}
					}
					// Consumed: one outcome belongs to one call.
					delete(commandAt, id)
				}
			}
			if out = strings.TrimSpace(out); out == "" {
				return
			}
			cur.Touch(t)
			cur.Messages = append(cur.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
		}
	})
	var out []model.Session
	if len(s.Messages) > 0 {
		out = append(out, s)
	}
	// The same rule as Claude Code's sub-agent files: by default the task,
	// the last turns and what the run changed; all of it with
	// DEJA_INCLUDE_SUBAGENTS=1, none of it with =0.
	for _, c := range children {
		if SubagentsOff() || copied[c.ID] || len(c.Messages) == 0 {
			continue
		}
		if !SubagentsWhole() {
			c.Messages = KeepSubagentTail(c.Messages)
		}
		c.Parent, c.Project = s.ID, s.Project
		out = append(out, *c)
	}
	return out, err
}

// jsonInt reads a number the scanner decoded with UseNumber, and the two other
// shapes a store can hold it in.
func jsonInt(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		// A store that wrote the code as 1.0 says the same thing.
		if f, err := n.Float64(); err == nil && f == float64(int(f)) {
			return int(f), true
		}
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true
		}
	}
	return 0, false
}

// copilotShellExitRe is the trailer Copilot writes under a shell run's output:
// "<shellId: 0 completed with exit code 1>". The tool's own documentation
// mentions the words "exit code" in prose, which is why this asks for the
// whole shape rather than the phrase.
var copilotShellExitRe = regexp.MustCompile(`<shellId:[^>]*completed with exit code (\d+)>`)

// copilotExitResumes sends an event log back for a whole read when its tail
// holds the failed exit of a command started before it (#4443).
var copilotExitResumes = resumesUnlessAnswering(`"tool.execution_`, func(m map[string]any) ([]string, string) {
	data, _ := m["data"].(map[string]any)
	id, _ := data["toolCallId"].(string)
	switch typ, _ := m["type"].(string); typ {
	case "tool.execution_start":
		return []string{id}, ""
	case "tool.execution_complete":
		result, _ := data["result"].(map[string]any)
		out, _ := result["content"].(string)
		if copilotExitCode(data, out) > 0 {
			return nil, id
		}
	}
	return nil, ""
})

// copilotResumes also reads the log whole when its tail closes a sub-agent
// run, ends or reopens the session, or holds a fork's note. A run cut off
// without its subagent.completed (a crash, Ctrl-C) heals at the session's
// shutdown or its next resume. A run is cut to its task, last turns and edits
// once it is whole, which a tail cannot do, and its records in between are
// appended as they come; a fork's note says which runs before it are copies of
// its source's (#5216, #5217). Every other tail, a run's middle included, is
// read on its own: a whole read on each pass of a long run cost seconds.
func copilotResumes(path string, offset int64) bool {
	if !copilotExitResumes(path, offset) {
		return false
	}
	ok := true
	_ = scanJSONLBytes(path, offset, func(line []byte) {
		if ok && (bytes.Contains(line, []byte(`"subagent.completed"`)) || bytes.Contains(line, []byte(`"infoType":"fork"`)) ||
			bytes.Contains(line, []byte(`"session.shutdown"`)) || bytes.Contains(line, []byte(`"session.resume"`))) {
			ok = false
		}
	})
	return ok
}

// copilotExitCode reads what a shell call actually did. The telemetry carries
// the number when Copilot recorded one; the trailer under the output is the
// fallback, and both are absent for a call that is not a shell run.
func copilotExitCode(data map[string]any, out string) int {
	if tel, ok := data["toolTelemetry"].(map[string]any); ok {
		if metrics, ok := tel["metrics"].(map[string]any); ok {
			// The scanner decodes with UseNumber, so a JSON number arrives as
			// json.Number and never as float64: asserting the latter made this
			// whole path dead code and left every marker to the trailer below
			// (review of #3369).
			if code, ok := jsonInt(metrics["exit_code"]); ok {
				return code
			}
		}
	}
	if m := copilotShellExitRe.FindStringSubmatch(out); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil {
			return code
		}
	}
	return 0
}

// copilotHeadCWD is the cwd session.start records, read off the first lines
// only.
func copilotHeadCWD(path string) string { return copilotReadHead(path).cwd }

type copilotHead struct {
	id, cwd string
	started time.Time
}

// copilotReadHead is what session.start records, read off the first lines
// only.
func copilotReadHead(path string) copilotHead {
	f, err := os.Open(path)
	if err != nil {
		return copilotHead{}
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for i := 0; i < 8 && sc.Scan(); i++ {
		var rec struct {
			Type      string `json:"type"`
			Timestamp any    `json:"timestamp"`
			Data      struct {
				SessionID string `json:"sessionId"`
				StartTime any    `json:"startTime"`
				Context   struct {
					CWD string `json:"cwd"`
				} `json:"context"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Type == "session.start" {
			started := parseTimeAny(rec.Data.StartTime)
			if started.IsZero() {
				started = parseTimeAny(rec.Timestamp)
			}
			return copilotHead{id: rec.Data.SessionID, cwd: rec.Data.Context.CWD, started: started}
		}
	}
	return copilotHead{}
}

// copilotProjectName is the recorded working directory's project, the last two
// segments; a Windows path from a synced store reads the same on any host.
func copilotProjectName(cwd string) string { return cwdProjectName(cwd) }
