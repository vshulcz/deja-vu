package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// commandExits joins a command record to the result that closes its call. The
// call and its result are separate parts, often in separate messages, and
// only the result says how the command ended; a reader that emitted the two
// without joining them stored every failure as a bare `$ cmd`, which reads the
// same as a run whose result never arrived (#4496, #4502, #4505, #4507).
type commandExits map[string][]int

// note files the command records appended to msgs from index from on under the
// calls they were read from, in order. calls is what commandCallsIn read off
// the same parts, so the two line up one to one; when they do not, nothing is
// noted rather than a status pinned to the wrong command.
func (c commandExits) note(msgs []model.Message, from int, calls []claudeCommand) {
	var at []int
	for i := from; i < len(msgs); i++ {
		if msgs[i].Role == RoleCommand {
			at = append(at, i)
		}
	}
	if len(at) != len(calls) {
		return
	}
	for k, i := range at {
		if calls[k].ID != "" {
			c[calls[k].ID] = append(c[calls[k].ID], i)
		}
	}
}

// stamp marks the commands of call id with the code their result reported, in
// the marker every other harness writes. cmd, when set, picks the one command
// of a batch the code belongs to: the first of them not stamped yet, since a
// batch that runs a command twice reports each run in order.
func (c commandExits) stamp(msgs []model.Message, id, cmd string, code int) {
	for _, i := range c[id] {
		if i >= len(msgs) || strings.Contains(msgs[i].Text, "  → exit ") {
			continue
		}
		if cmd != "" && msgs[i].Text != "$ "+cmd {
			continue
		}
		msgs[i].Text += fmt.Sprintf("  → exit %d", code)
		if cmd != "" {
			return
		}
	}
}

// joinResultExits is the join for a transcript in Claude's blocks: a tool_use
// with an id in one message, and a tool_result naming it under tool_use_id in
// a later one. It notes the commands blocks' calls appended to msgs from index
// from on, and stamps those a result among blocks reports on, with the code
// read off that result.
func joinResultExits(msgs []model.Message, from int, blocks any, d toolDialect, exits commandExits, code func(result map[string]any) (int, bool)) {
	if !IndexCommands() {
		return
	}
	exits.note(msgs, from, commandCallsIn(blocks, d))
	items, _ := blocks.([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok || m["type"] != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if _, ok := exits[id]; !ok {
			continue
		}
		if n, ok := code(m); ok {
			exits.stamp(msgs, id, "", n)
		}
		// A result answers its call once: a client that hands an id out
		// again, as call_1, means a later call by it.
		delete(exits, id)
	}
}

// commandCallsIn is commandsIn with the id of the call each command came from,
// so a reader can stamp the outcome when the result arrives.
func commandCallsIn(v any, d toolDialect) []claudeCommand {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []claudeCommand
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || !d.isShellTool(name) {
			continue
		}
		id := ""
		if m, ok := it.(map[string]any); ok {
			id, _ = m["id"].(string)
		}
		for _, cmd := range commandStrings(in, d) {
			if !worthIndexing(cmd) {
				continue
			}
			out = append(out, claudeCommand{ID: id, Text: "$ " + cmd})
		}
	}
	return out
}

// statusCode reads N off a status line a harness writes around a command's
// output, "<prefix>N<suffix>": Claude's "Exit code 1", pi's and goose's
// "Command exited with code 1", Cline's "Command failed with exit code 1.".
// The line must be the whole status, so output that merely mentions an exit
// code is not taken for one.
func statusCode(line, prefix, suffix string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, suffix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && rest != "" && rest[0] != '+' && rest[0] != '-'
}

// lastLine is firstLine from the other end, where pi and goose put the status
// they add after a command's output.
func lastLine(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	return strings.TrimSpace(s[strings.LastIndexByte(s, '\n')+1:])
}

// claudeExitResumes is the #4443 rule for Claude's format, which Cherry Studio
// writes too: a tail holding the result of a command call stored already is
// read whole, or the call never gets the exit its result names. A failed
// result says so on its own; a clean one stamps exit 0 only on a command, so
// the call it answers is looked up before the tail, and a clean answer to any
// other tool is let go, as it is for pi.
var claudeExitResumes = resumesUnlessSettling(`"tool_`, func(m map[string]any) (calls, failed, clean []string) {
	msg, _ := m["message"].(map[string]any)
	items, _ := msg["content"].([]any)
	for _, it := range items {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "tool_use":
			calls = append(calls, str(p["id"]))
		case "tool_result":
			id := str(p["tool_use_id"])
			if id == "" {
				continue
			}
			if bad, _ := p["is_error"].(bool); !bad {
				clean = append(clean, id)
			} else if claudeOutcome(id, true, p["content"]).Known {
				failed = append(failed, id)
			}
		}
	}
	return calls, failed, clean
}, func(line []byte) []string {
	var v struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &v) != nil {
		return nil
	}
	var ids []string
	for _, c := range claudeCommands(v.Message.Content) {
		ids = append(ids, c.ID)
	}
	return ids
})

// resumesUnlessSettling is resumesUnlessAnswering for a reader that stamps
// exit 0 on a command its clean result answers, as well as the code of a
// failed one. answers gives the calls a line makes, the ones it answers with
// a failure the reader stamps, and the ones it answers cleanly. A clean one
// is looked up before the tail, and only one that ran a command sends the
// file back for a whole read, or the command keeps no exit where a rebuild
// stamps 0 (#5527). commands gives the ids of the calls in a line that ran
// one.
func resumesUnlessSettling(hint string, answers func(m map[string]any) (calls, failed, clean []string), commands func(line []byte) []string) func(string, int64) bool {
	return func(path string, offset int64) bool {
		if offset <= 0 {
			return true
		}
		made := map[string]bool{}
		var clean []string
		ok := true
		_ = scanJSONLHinted(path, offset, []byte(hint), func(m map[string]any) {
			if !ok {
				return
			}
			calls, failed, settled := answers(m)
			for _, id := range calls {
				made[id] = true
			}
			for _, id := range failed {
				ok = ok && made[id]
			}
			for _, id := range settled {
				if !made[id] {
					clean = append(clean, id)
				}
			}
		})
		return ok && !commandCallBefore(path, offset, clean, commands)
	}
}

// callWindow is how far before a tail commandCallBefore looks for the call a
// clean result answers. A pass between a call and its result leaves the call
// among the last lines it read.
const callWindow = 4 << 20

// commandCallBefore reports whether one of ids is a call that ran a command,
// in the window of the file before end. Only the lines naming an id are
// decoded. A call not found there is taken for one that ran no command, as
// before.
func commandCallBefore(path string, end int64, ids []string, commands func(line []byte) []string) bool {
	if len(ids) == 0 || !IndexCommands() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	start := max(0, end-callWindow)
	buf := make([]byte, end-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return false
	}
	for _, id := range ids {
		quoted := []byte(`"` + id + `"`)
		for rest := buf; ; {
			i := bytes.Index(rest, quoted)
			if i < 0 {
				break
			}
			from := bytes.LastIndexByte(rest[:i], '\n') + 1
			to := len(rest)
			if j := bytes.IndexByte(rest[i:], '\n'); j >= 0 {
				to = i + j
			}
			if slices.Contains(commands(bytes.TrimPrefix(rest[from:to], []byte("\ufeff"))), id) {
				return true
			}
			rest = rest[to:]
		}
	}
	return false
}

// gooseExitResumes is the same rule for goose's jsonl sessions, whose reader
// stamps the exit_code of a clean run too.
var gooseExitResumes = resumesUnlessSettling(`"tool`, func(m map[string]any) (calls, failed, clean []string) {
	items, _ := m["content"].([]any)
	for _, it := range items {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "toolRequest":
			calls = append(calls, str(p["id"]))
		case "toolResponse":
			if code, ok := gooseExitCode(p); ok && str(p["id"]) != "" {
				if code != 0 {
					failed = append(failed, str(p["id"]))
				} else {
					clean = append(clean, str(p["id"]))
				}
			}
		}
	}
	return calls, failed, clean
}, func(line []byte) []string {
	return gooseParts(decodeJSONLine(line)["content"]).callIDs
})

// qwenExitResumes is the same rule for Qwen Code, which writes Gemini's parts:
// a functionCall in one record, its functionResponse in a tool_result record.
var qwenExitResumes = resumesUnlessAnswering(`"function`, func(m map[string]any) ([]string, string) {
	msg, _ := m["message"].(map[string]any)
	items, _ := msg["parts"].([]any)
	var calls []string
	for _, it := range items {
		p, _ := it.(map[string]any)
		if call, ok := p["functionCall"].(map[string]any); ok {
			calls = append(calls, str(call["id"]))
		}
		resp, ok := p["functionResponse"].(map[string]any)
		if !ok {
			continue
		}
		r, _ := resp["response"].(map[string]any)
		out, _ := r["output"].(string)
		errOut, _ := r["error"].(string)
		if id := str(resp["id"]); id != "" && (geminiExitCode(out) > 0 || geminiExitCode(errOut) > 0) {
			return calls, id
		}
	}
	return calls, ""
})
