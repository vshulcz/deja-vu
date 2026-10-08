package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A transcript an agent deleted stays in the index (#2970), and `deja resume
// --write-back` writes it back in the agent's own format so the agent can
// reopen it (#4617). The index holds each turn's role, text and time, not the
// tool calls, their output or the agent's own bookkeeping, so the file carries
// the conversation and the agent reopens it with that as its history.

// WriteBackFile is a transcript rebuilt from the index: the bytes, the file
// they go to, and the store directory that file has to stay inside.
type WriteBackFile struct {
	Path  string
	Root  string
	Data  []byte
	Turns int
}

// WriteBackRefusal says why a session cannot be written back, naming what the
// index lacks or what the format needs.
type WriteBackRefusal struct {
	Harness string
	Reason  string
}

func (e *WriteBackRefusal) Error() string { return e.Reason }

type writeBackFormat struct {
	// roots are the store directories a written file may land in.
	roots func() []string
	// render builds the file. turns holds the user and assistant turns only,
	// each with a time.
	render func(s model.Session, turns []model.Message) (path string, data []byte, err error)
}

// writeBackFormats holds a format per harness. A harness's file registers
// its own in init, beside its reader's knowledge of the format.
var writeBackFormats = map[string]writeBackFormat{
	"claude": {roots: claudeWriteBackRoots, render: renderClaude},
	"codex":  {roots: codexWriteBackRoots, render: renderCodex},
}

// registerWriteBack adds a harness's format.
func registerWriteBack(harness string, roots func() []string, render func(model.Session, []model.Message) (string, []byte, error)) {
	writeBackFormats[harness] = writeBackFormat{roots: roots, render: render}
}

// CanWriteBack reports whether deja writes sessions of harness back.
func CanWriteBack(harness string) bool {
	_, ok := writeBackFormats[harness]
	return ok
}

// WriteBackHarnesses lists the harnesses a session can be written back for.
func WriteBackHarnesses() []string {
	out := make([]string, 0, len(writeBackFormats))
	for h := range writeBackFormats {
		out = append(out, h)
	}
	return out
}

// writeBackLacks is what the index does not hold for a harness whose store
// would need it, or why deja does not write there, said to the person instead
// of writing a file the agent cannot open.
var writeBackLacks = map[string]string{}

// refuseWriteBack records why a harness's sessions are not written back.
func refuseWriteBack(harness, reason string) {
	writeBackLacks[harness] = reason
}

// WriteBackLacks is the reason a harness is not written back, or "".
func WriteBackLacks(harness string) string {
	return writeBackLacks[harness]
}

// RenderWriteBack builds the transcript for s in its harness's format. It does
// not touch the disk; the caller checks the target and writes it.
func RenderWriteBack(s model.Session) (WriteBackFile, error) {
	f, ok := writeBackFormats[s.Harness]
	if !ok {
		reason := writeBackLacks[s.Harness]
		if reason == "" {
			reason = "deja does not write " + s.Harness + " sessions back"
		}
		return WriteBackFile{}, &WriteBackRefusal{Harness: s.Harness, Reason: reason}
	}
	turns := writeBackTurns(s)
	if len(turns) == 0 {
		return WriteBackFile{}, &WriteBackRefusal{Harness: s.Harness, Reason: "the index holds no user or assistant turn for this session, only tool records, so there is no conversation to write"}
	}
	path, data, err := f.render(s, turns)
	if err != nil {
		return WriteBackFile{}, err
	}
	root := writeBackRootOf(path, f.roots())
	if root == "" {
		return WriteBackFile{}, &WriteBackRefusal{Harness: s.Harness, Reason: "the session's file " + path + " is not under " + s.Harness + "'s store, so deja will not write there"}
	}
	return WriteBackFile{Path: path, Root: root, Data: data, Turns: len(turns)}, nil
}

// writeBackTurns keeps the conversation: what the person said and what the
// agent answered. Tool output, commands, edits and file lists are deja's own
// records and have no place in the agent's history as plain turns. A turn
// with no time takes the one before it, then the session's start.
func writeBackTurns(s model.Session) []model.Message {
	var out []model.Message
	last := s.Started
	if last.IsZero() {
		last = s.Updated
	}
	if last.IsZero() {
		last = time.Unix(0, 0)
	}
	for _, m := range s.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		if m.Time.IsZero() || m.Time.Before(last) {
			m.Time = last
		}
		last = m.Time
		out = append(out, m)
	}
	return out
}

// writeBackRootOf is the root p lies inside, or "" when it is in none.
func writeBackRootOf(p string, roots []string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = filepath.Clean(r)
		rel, err := filepath.Rel(r, p)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		return r
	}
	return ""
}

// writeBackUUID is a stable v4-shaped id for turn i of a session, so writing
// the same session twice gives the same file.
func writeBackUUID(sessionID string, i int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("deja-write-back:%s:%d", sessionID, i)))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func writeBackStamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// jsonLines encodes each value on a line of its own, without escaping <, >
// and & the way json.Marshal does: the agents read either, and the file then
// reads like one they wrote.
func jsonLines(lines []any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// Claude Code: one JSON line per turn under projects/<encoded dir>/<id>.jsonl,
// chained by parentUuid. `claude --resume <id>` finds the file from any
// directory; cwd is written when deja can still resolve the project's
// directory and left out otherwise, which Claude Code accepts.
func claudeWriteBackRoots() []string {
	var out []string
	for _, r := range ClaudeRoots() {
		// The transcripts directory holds an older format of its own.
		if filepath.Base(r) == "transcripts" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func renderClaude(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Kind == "sidechain" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is a sub-agent run; Claude Code keeps those inside the parent's transcript and does not reopen one on its own"}
	}
	if !strings.HasSuffix(s.Path, ".jsonl") || strings.TrimSuffix(filepath.Base(s.Path), ".jsonl") != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no <id>.jsonl path for this session, which is where `claude --resume` looks"}
	}
	cwd := ClaudeSessionDir(s.Path)
	var lines []any
	parent := any(nil)
	for i, m := range turns {
		id := writeBackUUID(s.ID, i)
		line := map[string]any{
			"parentUuid":  parent,
			"isSidechain": false,
			"userType":    "external",
			"sessionId":   s.ID,
			"type":        m.Role,
			"uuid":        id,
			"timestamp":   writeBackStamp(m.Time),
		}
		if cwd != "" {
			line["cwd"] = cwd
		}
		if m.Role == "user" {
			line["message"] = map[string]any{"role": "user", "content": m.Text}
		} else {
			line["message"] = map[string]any{
				"id":            "msg_" + strings.ReplaceAll(id, "-", ""),
				"type":          "message",
				"role":          "assistant",
				"model":         "deja-write-back",
				"content":       []any{map[string]any{"type": "text", "text": m.Text}},
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			}
		}
		lines = append(lines, line)
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// Codex: a rollout under sessions/YYYY/MM/DD that opens with session_meta.
// The index keeps the project's name, not the directory codex recorded, so
// cwd comes from codex's own thread table while the thread is still listed
// there, else from the files the session itself worked on, else from the one
// directory codex recorded for the project in its other threads. A session
// with none of these is refused: codex lists a session for the directory in
// its session_meta. Each turn goes in twice, as a response item (what codex sends the
// model on resume) and as an event (what its history view shows), the way
// codex writes them.
func codexWriteBackRoots() []string {
	var out []string
	for _, r := range CodexRoots() {
		out = append(out, filepath.Join(r, "sessions"), filepath.Join(r, "archived_sessions"))
	}
	return out
}

func renderCodex(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Project == "history" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is a one-off codex exec entry from history.jsonl, with no rollout to write"}
	}
	// A compressed rollout goes back uncompressed: codex reads either, and
	// turns one back into .jsonl before it appends anyway.
	path := strings.TrimSuffix(s.Path, ".zst")
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no rollout-….jsonl path for this session, which is what `codex resume` looks for"}
	}
	cwd := codexWriteBackCWD(s)
	if cwd == "" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "codex's session_meta needs the directory the session ran in; the index keeps only the project's name, and neither codex's state database nor any file path in the session names that directory"}
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	lines := []any{map[string]any{
		"timestamp": writeBackStamp(start),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":          s.ID,
			"timestamp":   writeBackStamp(start),
			"cwd":         cwd,
			"originator":  "deja",
			"cli_version": "0.0.0",
			"source":      "cli",
		},
	}}
	for _, m := range turns {
		kind, event := "input_text", "user_message"
		if m.Role == "assistant" {
			kind, event = "output_text", "agent_message"
		}
		at := writeBackStamp(m.Time)
		lines = append(lines,
			map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{
				"type": "message", "role": m.Role,
				"content": []any{map[string]any{"type": kind, "text": m.Text}},
			}},
			map[string]any{"timestamp": at, "type": "event_msg", "payload": map[string]any{
				"type": event, "message": m.Text,
			}},
		)
	}
	data, err := jsonLines(lines)
	return path, data, err
}

// codexThreadCWD is the directory codex's state database recorded for a
// thread, or "" when no database lists it. Deleting a rollout leaves its row.
func codexThreadCWD(id string) string {
	if !codexThreadIDPattern.MatchString(id) {
		return ""
	}
	for _, root := range CodexRoots() {
		dbs, _ := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
		for _, db := range dbs {
			out, err := sqliteOutput(db, "select cwd from threads where id='"+id+"' limit 1")
			if err != nil {
				continue
			}
			if cwd := strings.TrimSpace(string(out)); cwd != "" {
				return cwd
			}
		}
	}
	return ""
}

var codexThreadIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// codexWriteBackCWD is the directory a codex session ran in, from the first
// record that still names it.
func codexWriteBackCWD(s model.Session) string {
	if cwd := codexThreadCWD(s.ID); cwd != "" {
		return cwd
	}
	if cwd := sessionDirFromPaths(s); cwd != "" {
		return cwd
	}
	return codexProjectCWD(s.Project)
}

// sessionDirFromPaths is the directory a session ran in, read off the
// absolute paths of the files it touched: the nearest parent whose project
// name is the session's. Most votes wins, so one file outside the checkout
// does not move it.
func sessionDirFromPaths(s model.Session) string {
	if s.Project == "" || s.Project == "-" {
		return ""
	}
	votes := map[string]int{}
	best := ""
	for _, m := range s.Messages {
		var paths []string
		switch m.Role {
		case RoleFiles:
			paths = strings.Split(m.Text, "\n")
		case RoleEdit, RoleWrote:
			paths = []string{firstLine(m.Text)}
		default:
			continue
		}
		for _, p := range paths {
			p = strings.TrimSpace(p)
			if !isAbsolutePath(p) {
				continue
			}
			for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
				if cwdProjectName(dir) == s.Project {
					votes[dir]++
					if votes[dir] > votes[best] {
						best = dir
					}
					break
				}
			}
		}
	}
	return best
}

// parentPath drops the last segment of p in either separator convention, or
// gives "" at the root.
func parentPath(p string) string {
	p = strings.TrimRight(p, `/\`)
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 || (i == 2 && p[1] == ':') {
		return ""
	}
	return p[:i]
}

// codexProjectCWD is the directory codex's state database records for other
// threads of the same project, when exactly one directory has that name.
func codexProjectCWD(project string) string {
	if project == "" || project == "-" {
		return ""
	}
	found := ""
	for _, root := range CodexRoots() {
		dbs, _ := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
		for _, db := range dbs {
			out, err := sqliteOutput(db, "select distinct cwd from threads")
			if err != nil {
				continue
			}
			for _, cwd := range strings.Split(string(out), "\n") {
				cwd = strings.TrimSpace(cwd)
				if cwd == "" || cwdProjectName(cwd) != project || cwd == found {
					continue
				}
				if found != "" {
					return ""
				}
				found = cwd
			}
		}
	}
	return found
}
