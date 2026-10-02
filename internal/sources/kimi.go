package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
// tool.result) and become work records (#655). Only the main agent is indexed;
// sub-agents and think-parts are skipped by design (issue #248).

// KimiConfigDir is the native Kimi Code home. DEJA_KIMI_ROOT intentionally
// does not affect it because that variable only relocates reads.
func KimiConfigDir() string { return EnvPath("KIMI_CODE_HOME", filepath.Join(Home(), ".kimi-code")) }

func KimiRoot() string { return EnvPath("DEJA_KIMI_ROOT", KimiConfigDir()) }

func KimiSessionFiles() []string {
	return walkFiles(filepath.Join(KimiRoot(), "sessions"), func(p string) bool {
		return filepath.Base(p) == "wire.jsonl" && filepath.Base(filepath.Dir(p)) == "main"
	})
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
// by design (#248); doctor counts them as skipped rather than unread (#4473).
func KimiSubagentFile(p string) bool {
	dir := filepath.Dir(p)
	return filepath.Base(p) == "wire.jsonl" && filepath.Base(dir) != "main" &&
		filepath.Base(filepath.Dir(dir)) == "agents"
}

func LoadKimi() []model.Session { return parseFiles(KimiSessionFiles(), ParseKimiFile) }

func ParseKimiFile(path string) ([]model.Session, error) {
	return parseKimiFileFromOffset(path, 0)
}

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

// kimiState is the slice of state.json deja cares about.
type kimiState struct {
	Title     string `json:"title"`
	WorkDir   string `json:"workDir"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
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
	return st.WorkDir
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
	if st, ok := kimiSessionState(path); ok {
		s.Title = strings.TrimSpace(st.Title)
		s.Project = projectName(st.WorkDir)
		s.Touch(parseTimeAny(st.CreatedAt))
		s.Touch(parseTimeAny(st.UpdatedAt))
	}
	// A Bash call and its result are separate loop events joined by
	// toolCallId; the command record is kept by id so the exit status the
	// result reports can ride on it (#4262).
	shellAt := map[string]int{}
	// Streamed assistant text accumulates across content.part events and is
	// flushed on step.end — or at EOF, so a response mid-stream when the
	// indexer runs is not lost (the remainder lands on the next incremental
	// pass as its own message).
	var pending strings.Builder
	var pendingTime any
	flush := func() {
		if text := strings.TrimSpace(pending.String()); text != "" {
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: parseTimeAny(pendingTime)})
		}
		pending.Reset()
		pendingTime = nil
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		switch m["type"] {
		case "context.append_message":
			msg, _ := m["message"].(map[string]any)
			if msg == nil {
				return
			}
			role, _ := msg["role"].(string)
			if role != "user" && role != "assistant" {
				return
			}
			// Kimi appends the host's own lines under role user too — an
			// injected reminder, a hook's wrapped stdout, a background task's
			// notice — and names the author in origin.kind. Only the person's
			// are user turns; a message with no origin is an older protocol's
			// and was always the person's (#3199).
			if role == "user" && !kimiPersonsOrigin(msg["origin"]) {
				// The host's line still moves the clock: a session whose
				// last record is a hook's output ended when that arrived.
				s.Touch(parseTimeAny(m["time"]))
				return
			}
			if role == "assistant" {
				// Non-streamed assistant records exist in older protocols;
				// close any open step first to keep message order stable.
				flush()
			}
			text := kimiText(msg["content"])
			t := parseTimeAny(m["time"])
			s.Touch(t)
			if text != "" {
				s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			}
		case "context.append_loop_event":
			e, _ := m["event"].(map[string]any)
			if e == nil {
				return
			}
			switch e["type"] {
			case "step.begin":
				flush()
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
					s.Touch(t)
					if pendingTime == nil {
						pendingTime = m["time"]
					}
				}
				pending.WriteString(text)
			case "step.end":
				flush()
			case "tool.call":
				// A call sits between the text parts of its step, so the
				// pending stream is flushed before the record to keep the
				// words that led to the call ahead of it. The flush happens
				// only when the call yields a record: with every DEJA_INDEX_*
				// switch off, the messages come out exactly as they did
				// before tool events were read.
				name, _ := e["name"].(string)
				args, _ := e["args"].(map[string]any)
				if name == "" || args == nil {
					return
				}
				part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
				t := parseTimeAny(m["time"])
				var records []model.Message
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
				shell := -1
				if IndexCommands() {
					for _, cmd := range commandsIn(part, kimiDialect) {
						shell = len(records)
						records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
					}
				}
				id, _ := e["toolCallId"].(string)
				// An id seen again belongs to this call now, recorded or not.
				delete(shellAt, id)
				if len(records) == 0 {
					return
				}
				flush()
				s.Touch(t)
				if shell >= 0 && id != "" {
					shellAt[id] = len(s.Messages) + shell
				}
				s.Messages = append(s.Messages, records...)
			case "tool.result":
				// Every observed result shape carries its text under
				// `output` — plain, with a note, truncated, or flagged
				// isError. The error ones are kept on purpose: the error a
				// command hit is exactly what a later search reaches for.
				r, _ := e["result"].(map[string]any)
				id, _ := e["toolCallId"].(string)
				if i, ok := shellAt[id]; ok {
					if code := kimiExitCode(r); code > 0 {
						s.Messages[i].Text += fmt.Sprintf("  → exit %d", code)
					}
					delete(shellAt, id)
				}
				if !IndexToolOutput() {
					return
				}
				out, _ := r["output"].(string)
				if out = strings.TrimSpace(out); out == "" {
					return
				}
				t := parseTimeAny(m["time"])
				flush()
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
			}
		}
	})
	flush()
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kimiTailResumes reads wire.jsonl whole when either rule asks: the tail
// answers a call made before it (#4443), or it goes on with a reply streamed
// across the offset (#4445).
func kimiTailResumes(path string, offset int64) bool {
	return kimiResumes(path, offset) && KimiResumes(path, offset)
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

// kimiText joins the text parts of an append_message content array.
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
		if t, _ := p["text"].(string); t != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	return strings.TrimSpace(b.String())
}
