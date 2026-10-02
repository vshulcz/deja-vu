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

// DeepSeek Harness (`dsh`) keeps one append-only log per session under
// $DSH_HOME/sessions/<workspace-slug>/session-<uuid>/. The log was
// session.jsonl.zstd when this reader was written; dsh has since moved to
// session.v3.jsonl.zstd with the same records, and a session directory can keep
// a header-only file under the old name beside its v3 log. The
// file is a JSONL stream written as consecutive zstd frames by default, with
// raw lines available as a configuration; both are read here, chosen by the
// extension the harness wrote.
//
// The first line is the session header (id, createdAt, cwd). Every line after
// it is one event: {type, seq, time, data}. Three of those types carry the
// conversation.
//
// A person's turn is `user/message` with data.source.kind == "user". The same
// type also carries what plugins splice in — the sandbox policy snapshot, the
// skill catalogue — under a different source kind, and those are the harness
// talking to itself, not history worth recalling.
//
// The agent's turn is written twice. It streams as `assistant/chunk` deltas —
// and long runs of those are packed into rows of another type entirely, so the
// stream alone is not readable without unpacking it — and then lands complete
// in one `assistant/message` whose data.message.content is a block array. The
// complete one is what deja reads; the deltas are the fallback for a run that
// was interrupted before it landed.
//
// Reasoning blocks in that array are the model thinking out loud, not what it
// told the person, so they stay out of the transcript.
//
// A tool call is written twice too: as a tool-call block in that
// assistant/message and as its own `tool/call` event. Only the event is read,
// so a call is counted once (#4291). What the call is credited with waits for
// its `tool/result`: an edit or write counts only when the result came back
// and is not an error (a denied or aborted call comes back as one), and a bash
// result carries its exit status as a trailing `[exit code: N]` marker rather
// than as an error.
//
// Tool output is its own event, `tool/result`, whose content nests a
// tool-result block around the text.
//
// Verified against dsh 0.1.1-rc.2 driven by a local model, over sessions that
// answered, called a tool, and failed before answering.

// DSHHome is the harness's own home directory, following its DSH_HOME variable.
// dsh expands a leading ~ in it, so deja does too: taken literally,
// DSH_HOME=~/.dsh-alt sent deja to ./~/.dsh-alt (#4390).
func DSHHome() string {
	if p := os.Getenv("DSH_HOME"); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(Home(), ".dsh")
}

// DeepSeekRoot is where dsh keeps session logs. DEJA_DEEPSEEK_ROOT relocates
// the read for tests and for a relocated install.
func DeepSeekRoot() string {
	return EnvPath("DEJA_DEEPSEEK_ROOT", filepath.Join(DSHHome(), "sessions"))
}

// deepSeekLogNames is every name dsh gives a session log. Discovery and the
// incremental index both match on this one list, so a new name cannot reach
// one of them and miss the other.
var deepSeekLogNames = []string{
	"session.jsonl", "session.jsonl.zstd",
	"session.v3.jsonl", "session.v3.jsonl.zstd",
}

func isDeepSeekLog(p string) bool {
	for _, name := range deepSeekLogNames {
		if hasBase(p, name) {
			return true
		}
	}
	return false
}

// DeepSeekSessionFiles leaves out Cherry Studio's dsh store. Cherry runs dsh
// with DSH_HOME pointed into its own data dir, so a deja it starts inherits that
// root and listed those logs under both harnesses (#4342).
func DeepSeekSessionFiles() []string {
	files := walkFiles(DeepSeekRoot(), isDeepSeekLog)
	cherry := cherryStudioDshRoots()
	if len(cherry) == 0 {
		return files
	}
	out := files[:0]
	for _, p := range files {
		if !underAnyRoot(p, cherry) {
			out = append(out, p)
		}
	}
	return out
}

func LoadDeepSeek() []model.Session {
	return parseFiles(DeepSeekSessionFiles(), ParseDeepSeekFile)
}

func ParseDeepSeekFile(path string) ([]model.Session, error) {
	raw, err := readDeepSeekLog(path)
	if err != nil {
		return nil, err
	}
	s := model.Session{
		Harness: "deepseek",
		ID:      strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "session-"),
		Project: "-",
		Path:    path,
	}
	// Deltas of the step being streamed, kept only until that step's complete
	// message arrives; a step that ends without one was interrupted, and then
	// the deltas are all there is.
	var pending []string
	var pendingAt time.Time
	cwd := ""
	calls := map[string]*deepSeekCall{}
	flush := func() {
		text := strings.TrimSpace(strings.Join(pending, ""))
		pending = nil
		if text == "" {
			return
		}
		// The answer is stamped with the last delta that made it: a message
		// without a time sorts to the beginning of history and is dropped by
		// every consumer that filters by date.
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: pendingAt})
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e map[string]any
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		typ, _ := e["type"].(string)
		data, _ := e["data"].(map[string]any)
		at := parseTimeAny(e["time"])
		switch typ {
		case "session":
			if id, _ := e["id"].(string); id != "" {
				s.ID = strings.TrimPrefix(id, "session-")
			}
			if dir, _ := e["cwd"].(string); dir != "" {
				cwd = dir
				s.Project = projectName(dir)
			}
			s.Touch(parseTimeAny(e["createdAt"]))
		case "session/title":
			// The last one wins. dsh names a session twice — a stand-in it cuts
			// out of the opening message, then the real one when its title
			// model answers — and the log is append-only, so the latest event
			// is the name the harness is showing. Keeping the first listed
			// every dsh session under a sentence cut mid-phrase (#2551).
			if title, _ := data["title"].(string); title != "" {
				s.Title = title
			}
		case "user/message":
			if !deepSeekSpokenByUser(data) {
				continue
			}
			flush()
			if text := deepSeekContentText(data["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: at})
				s.Touch(at)
			}
		case "assistant/chunk":
			chunk, _ := data["chunk"].(map[string]any)
			if kind, _ := chunk["type"].(string); kind == "text-delta" {
				if text, _ := chunk["text"].(string); text != "" {
					pending = append(pending, text)
					pendingAt = at
					s.Touch(at)
				}
			}
		case "text-chunks":
			// A run of three or more consecutive deltas is stored as one packed
			// row rather than as the events themselves, so a reader that knows
			// only `assistant/chunk` loses exactly the long answers.
			at := parseTimeAny(e["time0"])
			for _, part := range deepSeekPackedTexts(data["texts"]) {
				pending = append(pending, part)
				pendingAt = at
				s.Touch(at)
			}
		case "assistant/message":
			// The complete message supersedes whatever of it was streamed.
			pending = nil
			msg, _ := data["message"].(map[string]any)
			if text := deepSeekContentText(msg["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: at})
				s.Touch(at)
			}
		case "step/end", "turn/end":
			flush()
		case "tool/call":
			flush()
			now, call := deepSeekWorkRecords(data, cwd, at)
			if id, _ := data["callId"].(string); id != "" && call != nil {
				// The commands are the last of what the call stands for now.
				call.commandAt = len(s.Messages) + len(now) - call.commands
				calls[id] = call
			}
			s.Messages = append(s.Messages, now...)
		case "tool/result":
			flush()
			msg, _ := data["message"].(map[string]any)
			text := deepSeekToolText(msg["content"])
			source, _ := msg["source"].(map[string]any)
			id, _ := source["callId"].(string)
			if call := calls[id]; call != nil && !deepSeekResultFailed(msg["content"]) {
				delete(calls, id)
				s.Messages = append(s.Messages, call.settle(s.Messages, text)...)
			}
			if text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "tool-output", Text: text, Time: at})
				s.Touch(at)
			}
		}
	}
	flush()
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

// deepSeekDialect is dsh's tool vocabulary, read off its bundled tools: bash
// (pwsh on Windows) takes `command`, and read, read_image, write and edit take
// `file_path` with Claude's old_string/new_string/content.
var deepSeekDialect = toolDialect{
	pathKey:    "file_path",
	pathTools:  map[string]bool{"read": true, "read_image": true, "write": true, "edit": true, "str_replace_editor": true},
	shellTools: map[string]bool{"bash": true, "pwsh": true},
	editTools:  map[string]bool{"write": true, "edit": true, "str_replace_editor": true},
}

// deepSeekCall is one tool call waiting for its result: the records that only
// a clean result confirms, and where its command records sit.
type deepSeekCall struct {
	held []model.Message
	// commands records sit at commandAt and after.
	commands, commandAt int
	background          bool
	// view is a str_replace_editor view, whose path may be a directory; only
	// the result says which.
	view bool
}

// settle is what a clean result releases. The commands get the exit status the
// result's marker names, written the way every other harness's are; a result
// that was killed or timed out names none, and nothing is invented for it.
func (c *deepSeekCall) settle(msgs []model.Message, text string) []model.Message {
	if code, ok := deepSeekExitCode(text); ok && !c.background {
		for i := c.commandAt; i < c.commandAt+c.commands && i < len(msgs); i++ {
			if msgs[i].Role == RoleCommand && !strings.Contains(msgs[i].Text, "  → exit ") {
				msgs[i].Text += "  → exit " + code
			}
		}
	}
	// A view of a directory answers with a listing; a file it read answers
	// with its numbered content.
	if c.view && !strings.HasPrefix(text, "Here's the content of ") {
		return nil
	}
	return c.held
}

// deepSeekExitCode reads the status off a bash result: the last line is
// `[exit code: N]` for a nonzero exit, and a clean exit has no marker at all.
// A result over dsh's spill cap carries a "(Omitted N bytes. …)" notice after
// it, which is set aside first. A result ending in a marker that names no exit
// (timed out, killed, sandbox) or in the persistent shell's reset notice is
// not stamped; any other last line, bracketed or not, is the command's own.
func deepSeekExitCode(text string) (string, bool) {
	text = strings.TrimRight(text, "\n")
	if i := strings.LastIndex(text, "\n"); strings.Contains(text[i+1:], " Full formatted result stored at: ") {
		text = strings.TrimRight(text[:max(i, 0)], "\n")
		if text == "" {
			return "", false
		}
	}
	last := text[strings.LastIndex(text, "\n")+1:]
	if code, ok := strings.CutPrefix(last, "[exit code: "); ok {
		return strings.TrimSuffix(code, "]"), strings.HasSuffix(code, "]")
	}
	for _, p := range []string{"[timed out after ", "[killed by signal: ", "[sandbox: ", "[shell ", "The persistent bash shell was reset;"} {
		if strings.HasPrefix(last, p) {
			return "", false
		}
	}
	return "0", true
}

// deepSeekResultFailed reports whether a tool result is marked an error.
func deepSeekResultFailed(v any) bool {
	blocks, _ := v.([]any)
	for _, b := range blocks {
		block, _ := b.(map[string]any)
		if bad, _ := block["isError"].(bool); bad {
			return true
		}
	}
	return false
}

// deepSeekWorkRecords turns one tool/call event into work records: the files
// and command records it stands for now, and the edit and wrote records its
// result has to confirm. The call is rewritten into the tool_use shape the
// shared extractors read, as qwen's is.
func deepSeekWorkRecords(data map[string]any, cwd string, t time.Time) ([]model.Message, *deepSeekCall) {
	name, _ := data["name"].(string)
	args, _ := data["arguments"].(map[string]any)
	if raw, ok := data["arguments"].(string); ok {
		_ = json.Unmarshal([]byte(raw), &args)
	}
	if name == "" || args == nil {
		return nil, nil
	}
	call := &deepSeekCall{}
	if name == "str_replace_editor" {
		// The editor names its file `path` and its spans old_str, new_str and
		// file_text, and its `command` is view/create/str_replace, not a shell.
		call.view = args["command"] == "view"
		args = map[string]any{
			"file_path":  args["path"],
			"old_string": args["old_str"],
			"new_string": args["new_str"],
			"content":    args["file_text"],
		}
	}
	// dsh resolves a relative path against the session's directory, and so
	// does this: "retry.go" alone is out of reach of restore and blame.
	if p, _ := args["file_path"].(string); p != "" {
		args["file_path"] = resolveToolPath(p, cwd)
	}
	calls := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
	var now []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(calls, deepSeekDialect); p != "" {
			rec := model.Message{Role: RoleFiles, Text: p, Time: t}
			if call.view {
				call.held = append(call.held, rec)
			} else {
				now = append(now, rec)
			}
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, deepSeekDialect) {
			call.held = append(call.held, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, deepSeekDialect) {
			call.held = append(call.held, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexCommands() {
		cmds := commandsIn(calls, deepSeekDialect)
		call.commands = len(cmds)
		// A background run answers with an acknowledgement, not an exit.
		call.background, _ = args["run_in_background"].(bool)
		for _, cmd := range cmds {
			now = append(now, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
	return now, call
}

// deepSeekSpokenByUser separates what a person typed from what a plugin spliced
// into the same event type. Without it every session opens on the sandbox
// policy snapshot and the skill catalogue, which is the harness describing
// itself.
func deepSeekSpokenByUser(data map[string]any) bool {
	source, _ := data["source"].(map[string]any)
	kind, _ := source["kind"].(string)
	return kind == "user"
}

func deepSeekContentText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var out []string
	for _, p := range parts {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		// "reasoning" is the model thinking out loud; "text" is what it said.
		if t, _ := block["type"].(string); t != "" && t != "text" {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// deepSeekPackedTexts reads the texts of a packed chunk row. The row keeps the
// deltas verbatim in order; the timings beside them reconstruct each member's
// own clock, which a transcript does not need.
func deepSeekPackedTexts(v any) []string {
	parts, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if text, _ := p.(string); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// deepSeekToolText unwraps tool output, which nests one block array inside
// another: the outer block says which call this answers, the inner one carries
// the text the tool printed.
func deepSeekToolText(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		return deepSeekContentText(v)
	}
	var out []string
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if text := deepSeekContentText(block["content"]); text != "" {
			out = append(out, text)
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// readDeepSeekLog returns the log as plain JSONL. The default encoding is a
// chain of zstd frames, and deja carries no Go dependencies, so the frames go
// through the same `zstd` CLI the Zed store already needs — see SkipReason for
// what a machine without it is told.
func readDeepSeekLog(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".zstd") {
		return raw, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return zstdDecodeFile(path, "deepseek", raw)
}
