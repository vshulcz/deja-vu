package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kimi Code (github.com/MoonshotAI/kimi-code) keeps everything under
// $KIMI_CODE_HOME (default ~/.kimi-code):
//
//	session_index.jsonl
//	sessions/<workDirKey>/<sessionId>/state.json
//	sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl
//
// wire.jsonl is the append-only main-agent transcript. User turns arrive as
// context.append_message records; streamed assistant turns never do — they
// have to be reconstructed from step.begin → content.part → step.end loop
// events. Tool calls and their results arrive as loop events too (tool.call,
// tool.result) and become work records (#655). A sub-agent's
// agents/<agent-id>/wire.jsonl is read too, cut to its task, answer and
// changes unless DEJA_INCLUDE_SUBAGENTS=1 (#4483, #5229); think-parts are
// skipped by design (issue #248). A /btw side question runs in a fork
// of the main agent that state.json marks forkedFrom and the btw reminder
// opens; the person asked it, so it is read whatever the switch says (#4484).

// KimiConfigDir is the native Kimi Code home. DEJA_KIMI_ROOT intentionally
// does not affect it because that variable only relocates reads.
func KimiConfigDir() string { return EnvPath("KIMI_CODE_HOME", filepath.Join(Home(), ".kimi-code")) }

// KimiRoot is the Kimi Code config dir deja reads sessions from.
func KimiRoot() string { return EnvPath("DEJA_KIMI_ROOT", KimiConfigDir()) }

// KimiSessionFiles takes sub-agent logs by default, read the way Claude's and
// Qwen's are: the task, the last turns and the files the child changed, so
// blame names the sub-agent that wrote a file (#5229). DEJA_INCLUDE_SUBAGENTS=1
// keeps the whole run, and an Agent call with fork: true, whose log opens on a
// copy of main's context; =0 skips them all but a /btw.
func KimiSessionFiles() []string {
	whole := SubagentsWhole()
	off := SubagentsOff()
	forks := map[string]map[string]bool{}
	return walkFiles(filepath.Join(KimiRoot(), "sessions"), func(p string) bool {
		if filepath.Base(p) == "wire.jsonl" && filepath.Base(filepath.Dir(p)) == "main" {
			return true
		}
		if !KimiSubagentFile(p) {
			return false
		}
		if whole {
			return true
		}
		dir := kimiSessionDirOf(p)
		f, ok := forks[dir]
		if !ok {
			f = kimiForks(dir)
			forks[dir] = f
		}
		if !f[filepath.Base(filepath.Dir(p))] {
			return !off
		}
		return kimiHasBtw(p)
	})
}

// kimiSessionDirOf is the session directory of an agent's wire.jsonl at
// .../sessions/<workDirKey>/<sessionId>/agents/<agent-id>/wire.jsonl.
func kimiSessionDirOf(p string) string { return filepath.Dir(filepath.Dir(filepath.Dir(p))) }

// kimiForks lists the agents state.json records as forks of another agent.
// Kimi forks main for a /btw side question, and from 2.x for an Agent or
// AgentSwarm call with fork: true too; only the btw reminder in the log tells
// the two apart (#4484).
func kimiForks(sessionDir string) map[string]bool {
	var st struct {
		Agents map[string]struct {
			ForkedFrom string `json:"forkedFrom"`
		} `json:"agents"`
	}
	b, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return nil
	}
	out := map[string]bool{}
	for id, a := range st.Agents {
		if a.ForkedFrom != "" {
			out[id] = true
		}
	}
	return out
}

// kimiForkFile reports whether p is the log of an agent forked from another.
func kimiForkFile(p string) bool {
	return KimiSubagentFile(p) && kimiForks(kimiSessionDirOf(p))[filepath.Base(filepath.Dir(p))]
}

// kimiBtwFile reports whether p is a /btw fork's log.
func kimiBtwFile(p string) bool { return kimiForkFile(p) && kimiHasBtw(p) }

// KimiSubagentRun reports whether p is a sub-agent run's log, which is read as
// its task, answer and changes unless DEJA_INCLUDE_SUBAGENTS=1: a sub-agent
// file that is not a fork of main.
func KimiSubagentRun(p string) bool { return KimiSubagentFile(p) && !kimiForkFile(p) }

// kimiBtwTrigger reports whether an appended message is the reminder Kimi puts
// in a fork to open a /btw side question; what comes before it is main's
// context, copied (#4484). 0.28 marks it {kind: system_trigger, name: btw};
// 0.43 and 2.x {kind: injection, variant: btw}.
func kimiBtwTrigger(msg map[string]any) bool {
	o, _ := msg["origin"].(map[string]any)
	return msg["role"] == "user" &&
		(o["kind"] == "system_trigger" && o["name"] == "btw" || o["kind"] == "injection" && o["variant"] == "btw")
}

// kimiBtwSeen remembers which fork logs hold the btw reminder, by size and
// mtime, so the file list does not read every fork on every pass. A log that
// held it holds it for good: the file only grows.
var kimiBtwSeen = struct {
	sync.Mutex
	m map[string]kimiBtwMark
}{m: map[string]kimiBtwMark{}}

type kimiBtwMark struct {
	size, mod int64
	btw       bool
}

// kimiHasBtw reports whether the log at p holds the btw reminder.
func kimiHasBtw(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	kimiBtwSeen.Lock()
	m, ok := kimiBtwSeen.m[p]
	kimiBtwSeen.Unlock()
	if ok && (m.btw || m.size == fi.Size() && m.mod == fi.ModTime().UnixNano()) {
		return m.btw
	}
	btw := false
	eachLineFrom(p, 0, func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"btw"`)) {
			return true
		}
		if msg, _ := decodeJSONLine(line)["message"].(map[string]any); msg != nil && kimiBtwTrigger(msg) {
			btw = true
		}
		return !btw
	})
	kimiBtwSeen.Lock()
	kimiBtwSeen.m[p] = kimiBtwMark{fi.Size(), fi.ModTime().UnixNano(), btw}
	kimiBtwSeen.Unlock()
	return btw
}

// KimiSidecarFiles lists the per-session state.json the reader opens itself
// for the title and the working directory. doctor counted one per session as a
// transcript it could not read (#3309). The goal queue, upcoming-goals.json,
// sits beside it and is Kimi's own too (#4473), as is a background task's
// record, tasks/<task-id>.json in an agent's directory or the session's.
func KimiSidecarFiles() []string {
	return walkFiles(filepath.Join(KimiRoot(), "sessions"), func(p string) bool {
		base := filepath.Base(p)
		return base == "state.json" || base == "upcoming-goals.json" ||
			filepath.Base(filepath.Dir(p)) == "tasks" && strings.HasSuffix(base, ".json")
	})
}

// KimiSubagentFile reports whether p is a sub-agent's log, which Kimi writes at
// agents/<agent-id>/wire.jsonl beside agents/main. The reader leaves those out
// unless DEJA_INCLUDE_SUBAGENTS=1 (#248, #4483); doctor counts them as skipped
// rather than unread (#4473).
func KimiSubagentFile(p string) bool {
	dir := filepath.Dir(p)
	return filepath.Base(p) == "wire.jsonl" && filepath.Base(dir) != "main" &&
		filepath.Base(filepath.Dir(dir)) == "agents"
}

// LoadKimi reads every Kimi Code session on this machine.
func LoadKimi() []model.Session { return parseFiles(KimiSessionFiles(), ParseKimiFile) }

// ParseKimiFile reads one Kimi Code transcript.
func ParseKimiFile(path string) ([]model.Session, error) {
	return parseKimiFileFromOffset(path, 0)
}

// ParseKimiFileFromOffset reads a Kimi Code transcript from offset on.
func ParseKimiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseKimiFileFromOffset(path, offset)
}

// kimiDialect is Kimi Code's tool vocabulary, read off wire.jsonl transcripts
// its own CLI had just written. The names mirror Claude Code's — Kimi Code is
// built in that shape — but the file key is `path` where Claude's is
// `file_path`, and only Edit carries a replaced span; there is no MultiEdit.
// ReadMediaFile is Kimi's own path tool. The shell tool's argument key is
// `command`, which is what commandsIn already reads.
var kimiDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"Read": true, "Edit": true, "Write": true, "ReadMediaFile": true},
	shellTool: "Bash",
	// Write carries the whole file under `content`, which is the default
	// key — it only had to be counted as an edit for the written side to
	// reach the index (#595).
	editTools: map[string]bool{"Edit": true, "Write": true},
}

// kimiState is the slice of state.json deja cares about. Kimi Code 2.x
// renamed workDir to cwd and stamps in epoch milliseconds where 0.x wrote
// ISO strings (#5143).
type kimiState struct {
	Title     string `json:"title"`
	WorkDir   string `json:"workDir"`
	CWD       string `json:"cwd"`
	CreatedAt any    `json:"createdAt"`
	UpdatedAt any    `json:"updatedAt"`
	// ForkedFrom names the source of a 2.x /fork or `kimi fork`, which
	// copies each of the source's wire.jsonl into the new session (#5226).
	ForkedFrom string `json:"forkedFrom"`
}

// dir is the directory the session ran in, under either name.
func (st kimiState) dir() string {
	if st.WorkDir != "" {
		return st.WorkDir
	}
	return st.CWD
}

// kimiPersonsOrigin reports whether a message's origin says a person wrote it:
// kind "user", a skill or plugin command the person typed (Kimi marks those
// with trigger "user-slash"), or no origin at all — the same disposition
// Kimi's own context builder applies.
func kimiPersonsOrigin(origin any) bool {
	o, ok := origin.(map[string]any)
	if !ok {
		return true
	}
	kind, _ := o["kind"].(string)
	trigger, _ := o["trigger"].(string)
	return kind == "" || kind == "user" || strings.HasPrefix(kind, "user") || trigger == "user-slash"
}

// kimiSubagentTask reports whether a message is the task a sub-agent was
// handed: kimi-code opens the run, and every resume of it, with the parent's
// prompt under origin {kind: system_trigger, name: subagent}
// (AGENT_RUN_PROMPT_ORIGIN). It is the run's question, so it is its user turn;
// dropped with the host's other lines, the run was titled by its answer
// (#5227).
func kimiSubagentTask(origin any) bool {
	o, ok := origin.(map[string]any)
	return ok && o["kind"] == "system_trigger" && o["name"] == "subagent"
}

// kimiGitContext is the block the explore profile's promptPrefix puts ahead
// of the parent's prompt (collectGitContext): the branch, dirty files and log,
// or a self-closing tag outside a repository. It is Kimi's, not the task.
var kimiGitContext = regexp.MustCompile(`^\s*(?:<git-context\b[^>]*/>|(?s:<git-context\b.*?</git-context>))\s*`)

func kimiWithoutGitContext(t string) string {
	return strings.TrimSpace(kimiGitContext.ReplaceAllString(t, ""))
}

// kimiSessionState reads the state.json beside a wire.jsonl at
// .../sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl.
func kimiSessionState(path string) (st kimiState, ok bool) {
	sessionDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	b, err := os.ReadFile(filepath.Join(sessionDir, "state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return kimiState{}, false
	}
	return st, true
}

// KimiSessionDir is the directory a Kimi Code session was created in, for the
// cd in front of `kimi --session`: Kimi refuses a session from any other
// directory (#4274).
func KimiSessionDir(path string) string {
	if path == "" {
		return ""
	}
	st, _ := kimiSessionState(path)
	return st.dir()
}

// KimiForkSource is the session a Kimi Code session was forked from, by id:
// 2.x /fork copies a session into a new one and records the source as
// forkedFrom in the copy's state.json. Empty when the id is not a fork or not
// in the store.
func KimiForkSource(id string) string {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "session_") || strings.ContainsAny(id, `/\*?[`) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(KimiRoot(), "sessions", "*", id, "state.json"))
	for _, m := range matches {
		var st struct {
			ForkedFrom string `json:"forkedFrom"`
		}
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &st) == nil && st.ForkedFrom != "" {
			return st.ForkedFrom
		}
	}
	return ""
}

// kimiExit is the footer kimi-code's Bash tool writes on a non-zero exit,
// with the truncation note on the same line when the output was cut. A
// timeout or an interrupt ends with its own message and carries no code.
var kimiExit = regexp.MustCompile(`^Command failed with exit code: (\d+)\.`)

// kimiResumes sends a wire.jsonl back for a whole read when its tail holds the
// failed exit of a command called before it (#4443).
var kimiResumes = resumesUnlessAnswering(`"tool.`, func(m map[string]any) ([]string, string) {
	e, _ := m["event"].(map[string]any)
	id, _ := e["toolCallId"].(string)
	switch typ, _ := e["type"].(string); typ {
	case "tool.call":
		return []string{id}, ""
	case "tool.result":
		if r, _ := e["result"].(map[string]any); kimiExitCode(r) > 0 {
			return nil, id
		}
	}
	return nil, ""
})

// kimiExitCode reads the status off a Bash result: the footer on its last
// line, ahead of the saved-output reference a truncated result carries, and
// only on a result flagged isError, so a clean run whose output quotes the
// footer is not taken for a failure. 0 when there is none.
func kimiExitCode(r map[string]any) int {
	if isErr, _ := r["isError"].(bool); !isErr {
		return 0
	}
	out, _ := r["output"].(string)
	if i := strings.LastIndex(out, "\n\n[Full output saved]\n"); i >= 0 {
		out = out[:i]
	}
	out = strings.TrimRight(out, "\n")
	m := kimiExit.FindStringSubmatch(strings.TrimSpace(out[strings.LastIndex(out, "\n")+1:]))
	if m == nil {
		return 0
	}
	code, _ := strconv.Atoi(m[1])
	return code
}

func parseKimiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	// .../sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl
	sessionDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	s := model.Session{
		Harness: "kimi",
		ID:      filepath.Base(sessionDir),
		Path:    path,
	}
	child := KimiSubagentFile(path)
	// A fork opens with a copy of main's context, which is main's to index:
	// in a /btw fork only what follows the side-question reminder is read,
	// and a fork with none is that copy and nothing else (#4484).
	fork := child && kimiForkFile(path)
	cwd, forkedFrom := "", ""
	if st, ok := kimiSessionState(path); ok {
		s.Title = strings.TrimSpace(st.Title)
		s.Project = projectName(st.dir())
		cwd = st.dir()
		if f := strings.TrimSpace(st.ForkedFrom); f != "" && f != s.ID {
			forkedFrom = f
		}
		if !child {
			s.Touch(parseTimeAny(st.CreatedAt))
			s.Touch(parseTimeAny(st.UpdatedAt))
		}
	}
	if child {
		// A sub-agent sits under the session that spawned it and shares its
		// state.json: its own id, not the parent's, which would make it a
		// second copy of the parent; no title or span from the parent's
		// state (#4483). The agent's name leads, so the parent's id is not a
		// prefix of it: a whole id opens the newest session it prefixes.
		s.Kind = "subagent"
		s.Parent = s.ID
		s.ID = filepath.Base(filepath.Dir(path)) + "-" + s.Parent
		s.Title = ""
	} else if forkedFrom != "" {
		// A session /fork made: what it copied is its source's, read there.
		s.Kind, s.Parent = "fork", forkedFrom
	}
	w := &kimiWire{s: &s, cwd: cwd, child: child, fork: fork, shellAt: map[string]int{}}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		switch m["type"] {
		case "forked":
			// kimi-code's fork copies the source agent's records and closes
			// the copy with this one; only what follows is the fork's. Read as
			// its own, the copy put the source's turns, writes and sub-agent
			// runs in the fork a second time, and blame credited the fork
			// with the source's lines (#5226).
			if forkedFrom == "" {
				return
			}
			s.Messages = nil
			s.Started, s.Updated = time.Time{}, time.Time{}
			w.pending.Reset()
			w.pendingTime = nil
			clear(w.shellAt)
		case "context.apply_compaction":
			// The summary the compaction left in place of the turns it
			// dropped (kimi-code 0.28.1). summary is the model's text, an
			// older record's a whole message; contextSummary is that text
			// behind the line Kimi tells the model it was compacted with.
			text, _ := m["summary"].(string)
			if sm, ok := m["summary"].(map[string]any); ok {
				text = kimiText(sm["content"])
			}
			if strings.TrimSpace(text) == "" {
				text, _ = m["contextSummary"].(string)
			}
			if text = strings.TrimSpace(text); text != "" {
				w.flush()
				t := parseTimeAny(m["time"])
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: text, Time: t})
			}
		case "context.append_message":
			w.appendMessage(m)
		case "context.append_loop_event":
			w.loopEvent(m)
		}
	})
	w.flush()
	if w.btw {
		// The person asked it in the session it forked from; it is not a run
		// that session spawned.
		s.Kind = "fork"
	} else if fork && !SubagentsWhole() {
		return nil, err
	}
	if child && !fork && !SubagentsWhole() {
		s.Messages = KeepSubagentTail(s.Messages)
	}
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kimiWire is what one read of wire.jsonl carries from record to record.
type kimiWire struct {
	s           *model.Session
	cwd         string
	child, fork bool
	// btw is set once a /btw fork reaches its side-question reminder.
	btw bool
	// A Bash call and its result are separate loop events joined by
	// toolCallId; the command record is kept by id so the exit status the
	// result reports can ride on it (#4262).
	shellAt map[string]int
	// Streamed assistant text accumulates across content.part events and is
	// flushed on step.end — or at EOF, so a response mid-stream when the
	// indexer runs is not lost (the remainder lands on the next incremental
	// pass as its own message).
	pending     strings.Builder
	pendingTime any
}

// flush appends the streamed assistant text gathered so far as one message.
func (w *kimiWire) flush() {
	if text := strings.TrimSpace(w.pending.String()); text != "" {
		w.s.Messages = append(w.s.Messages, model.Message{Role: "assistant", Text: text, Time: parseTimeAny(w.pendingTime)})
	}
	w.pending.Reset()
	w.pendingTime = nil
}

// appendMessage reads a context.append_message record.
func (w *kimiWire) appendMessage(m map[string]any) {
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return
	}
	role, _ := msg["role"].(string)
	if role != "user" && role != "assistant" {
		return
	}
	if w.fork && kimiBtwTrigger(msg) {
		w.btw = true
		w.s.Messages = nil
		w.s.Started, w.s.Updated = time.Time{}, time.Time{}
		w.pending.Reset()
		w.pendingTime = nil
		clear(w.shellAt)
		return
	}
	// Kimi appends the host's own lines under role user too — an
	// injected reminder, a hook's wrapped stdout, a background task's
	// notice — and names the author in origin.kind. Only the person's
	// are user turns; a message with no origin is an older protocol's
	// and was always the person's (#3199).
	if role == "user" && !kimiPersonsOrigin(msg["origin"]) && (!w.child || !kimiSubagentTask(msg["origin"])) {
		// The host's line still moves the clock: a session whose
		// last record is a hook's output ended when that arrived.
		w.s.Touch(parseTimeAny(m["time"]))
		return
	}
	if role == "assistant" {
		// Non-streamed assistant records exist in older protocols;
		// close any open step first to keep message order stable.
		w.flush()
	}
	text := kimiText(msg["content"])
	if role == "user" && w.child && kimiSubagentTask(msg["origin"]) {
		text = kimiWithoutGitContext(text)
	}
	t := parseTimeAny(m["time"])
	w.s.Touch(t)
	if text != "" {
		w.s.Messages = append(w.s.Messages, model.Message{Role: role, Text: text, Time: t})
	}
}

// loopEvent reads a context.append_loop_event record: streamed text, step
// boundaries, tool calls and their results.
func (w *kimiWire) loopEvent(m map[string]any) {
	e, _ := m["event"].(map[string]any)
	if e == nil {
		return
	}
	switch e["type"] {
	case "step.begin":
		w.flush()
	case "content.part":
		p, _ := e["part"].(map[string]any)
		if p == nil || p["type"] != "text" {
			return
		}
		text, _ := p["text"].(string)
		if text == "" {
			return
		}
		if t := parseTimeAny(m["time"]); !t.IsZero() {
			w.s.Touch(t)
			if w.pendingTime == nil {
				w.pendingTime = m["time"]
			}
		}
		w.pending.WriteString(text)
	case "step.end":
		w.flush()
	case "tool.call":
		// A call sits between the text parts of its step, so the
		// pending stream is flushed before the record to keep the
		// words that led to the call ahead of it. The flush happens
		// only when the call yields a record: with every DEJA_INDEX_*
		// switch off, the messages come out exactly as they did
		// before tool events were read.
		records, shell, t, ok := kimiToolRecords(m, e, w.cwd)
		if !ok {
			return
		}
		id, _ := e["toolCallId"].(string)
		// An id seen again belongs to this call now, recorded or not.
		delete(w.shellAt, id)
		if len(records) == 0 {
			return
		}
		w.flush()
		w.s.Touch(t)
		if shell >= 0 && id != "" {
			w.shellAt[id] = len(w.s.Messages) + shell
		}
		w.s.Messages = append(w.s.Messages, records...)
	case "tool.result":
		// Every observed result shape carries its text under
		// `output` — plain, with a note, truncated, or flagged
		// isError. The error ones are kept on purpose: the error a
		// command hit is exactly what a later search reaches for.
		r, _ := e["result"].(map[string]any)
		id, _ := e["toolCallId"].(string)
		if i, ok := w.shellAt[id]; ok {
			if code := kimiExitCode(r); code > 0 {
				w.s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
			}
			delete(w.shellAt, id)
		}
		if !IndexToolOutput() {
			return
		}
		out, _ := r["output"].(string)
		if out = strings.TrimSpace(out); out == "" {
			return
		}
		t := parseTimeAny(m["time"])
		w.flush()
		w.s.Touch(t)
		w.s.Messages = append(w.s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
	}
}

// kimiToolRecords is what a tool.call yields: its path, write, edit and command
// records, the index of the last command among them or -1, and the record's
// time. ok is false for a call with no name or arguments.
func kimiToolRecords(m, e map[string]any, cwd string) (records []model.Message, shell int, t time.Time, ok bool) {
	name, _ := e["name"].(string)
	args, _ := e["args"].(map[string]any)
	if name == "" || args == nil {
		return nil, -1, time.Time{}, false
	}
	// Kimi resolves a relative path against the session's cwd; kept
	// bare, blame never matched the record to the file (#5228). A
	// leading ~, ~/ or ~\ is Kimi's to expand and stays as written.
	if p, _ := args[kimiDialect.pathKey].(string); p != "" && p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		if abs := resolveToolPath(p, cwd); abs != p {
			args = maps.Clone(args)
			args[kimiDialect.pathKey] = abs
		}
	}
	part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
	t = parseTimeAny(m["time"])
	if IndexToolPaths() {
		if p := toolPathsIn(part, kimiDialect); p != "" {
			records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(part, kimiDialect) {
			records = append(records, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(part, kimiDialect) {
			records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	shell = -1
	if IndexCommands() {
		for _, cmd := range commandsIn(part, kimiDialect) {
			shell = len(records)
			records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
	return records, shell, t, true
}

// kimiTailResumes reads wire.jsonl whole when either rule asks: the tail
// answers a call made before it (#4443), or it goes on with a reply streamed
// across the offset (#4445).
// A /btw fork is read whole too: the tail cannot say where the copied context
// ends (#4484).
func kimiTailResumes(path string, offset int64) bool {
	if offset > 0 && kimiBtwFile(path) {
		return false
	}
	// A fork read before Kimi wrote its state.json was stored as its source's
	// copy; the forked record arriving in the tail sends it back for a whole
	// read, which drops the copy (#5226).
	if offset > 0 && kimiTailHasForked(path, offset) {
		return false
	}
	return kimiResumes(path, offset) && KimiResumes(path, offset)
}

func kimiTailHasForked(path string, offset int64) bool {
	found := false
	eachLineFrom(path, offset, func(line []byte) bool {
		if bytes.Contains(line, []byte(`"forked"`)) && decodeJSONLine(line)["type"] == "forked" {
			found = true
			return false
		}
		return true
	})
	return found
}

// KimiResumes reports whether the tail of wire.jsonl can be appended to what
// is stored. A reply's content.part events are joined until the step ends, and
// a pass mid-step flushes what has arrived; when the tail goes on with that
// step, the file is read whole (#4445). A tool event can sit inside the
// stream, so one on either side of the offset counts as the stream going on,
// and so does a user turn after it, which a whole read places ahead of the
// reply it interrupted.
func KimiResumes(path string, offset int64) bool {
	if offset <= 0 {
		return true
	}
	open := false
	eachLineBefore(path, offset, func(line []byte) bool {
		switch kimiStreamEvent(line) {
		case kimiStreamGoesOn:
			open = true
			return false
		case kimiStreamEnds:
			return false
		}
		return true
	})
	if !open {
		return true
	}
	resumes := true
	eachLineFrom(path, offset, func(line []byte) bool {
		switch kimiStreamEvent(line) {
		case kimiStreamGoesOn, kimiUserTurn:
			resumes = false
			return false
		case kimiStreamEnds:
			return false
		}
		return true
	})
	return resumes
}

const (
	kimiStreamOther = iota
	kimiStreamGoesOn
	kimiStreamEnds
	kimiUserTurn
)

// kimiStreamEvent says what a wire.jsonl line does to a reply being streamed.
func kimiStreamEvent(line []byte) int {
	m := decodeJSONLine(line)
	switch m["type"] {
	case "context.append_message":
		msg, _ := m["message"].(map[string]any)
		switch {
		case msg == nil:
		case msg["role"] == "assistant":
			return kimiStreamEnds
		case msg["role"] == "user":
			return kimiUserTurn
		}
	case "context.append_loop_event":
		e, _ := m["event"].(map[string]any)
		switch e["type"] {
		case "step.begin", "step.end":
			return kimiStreamEnds
		case "tool.call", "tool.result":
			return kimiStreamGoesOn
		case "content.part":
			if p, _ := e["part"].(map[string]any); p != nil && p["type"] == "text" {
				if text, _ := p["text"].(string); text != "" {
					return kimiStreamGoesOn
				}
			}
		}
	}
	return kimiStreamOther
}

// kimiHookResultPart reports whether a text part is one hook's output as
// Kimi wraps it. 2.x hands those to the model as parts of the person's own
// message, ahead of what was typed and under origin user, where 0.28 gave them
// a message of their own (#5144). What a person types is a part of its own.
func kimiHookResultPart(t string) bool {
	t = strings.TrimSpace(t)
	return strings.HasPrefix(t, "<hook_result") && strings.HasSuffix(t, "</hook_result>")
}

// kimiText joins the text parts of an append_message content array, less the
// hooks' output.
func kimiText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var b strings.Builder
	for _, pv := range parts {
		p, ok := pv.(map[string]any)
		if !ok || p["type"] != "text" {
			continue
		}
		if t, _ := p["text"].(string); t != "" && !kimiHookResultPart(t) {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	return strings.TrimSpace(b.String())
}
